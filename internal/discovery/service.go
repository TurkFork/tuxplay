package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/grandcat/zeroconf"

	"tuxplay/internal/model"
	"tuxplay/internal/state"
)

type Service struct {
	store  *state.Store
	logger *slog.Logger
}

func New(store *state.Store, logger *slog.Logger) *Service {
	return &Service{
		store:  store,
		logger: logger.With("component", "discovery"),
	}
}

func (s *Service) Run(ctx context.Context) error {
	s.store.SetDiscoveryLive(true)
	defer s.store.SetDiscoveryLive(false)

	type serviceDef struct {
		name     string
		protocol string
	}

	services := []serviceDef{
		{name: "_airplay._tcp", protocol: "airplay"},
		{name: "_raop._tcp", protocol: "raop"},
	}

	errCh := make(chan error, len(services))
	for _, service := range services {
		go func(def serviceDef) {
			errCh <- s.browseLoop(ctx, def.name, def.protocol)
		}(service)
	}

	for range services {
		if err := <-errCh; err != nil && ctx.Err() == nil {
			return err
		}
	}

	return ctx.Err()
}

func (s *Service) browseLoop(ctx context.Context, serviceName string, protocol string) error {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return fmt.Errorf("create resolver for %s: %w", serviceName, err)
	}

	entries := make(chan *zeroconf.ServiceEntry)
	go func() {
		for entry := range entries {
			device := normalizeEntry(entry, protocol)
			s.store.UpsertDevice(device)
			s.logger.Debug("device discovered",
				"service", serviceName,
				"id", device.ID,
				"name", device.Name,
				"ip", device.Address,
				"port", device.Port,
			)
		}
	}()

	if err := resolver.Browse(ctx, serviceName, "local.", entries); err != nil {
		return fmt.Errorf("browse %s: %w", serviceName, err)
	}

	<-ctx.Done()
	return nil
}

func normalizeEntry(entry *zeroconf.ServiceEntry, protocol string) model.Device {
	instance := unescapeInstance(entry.Instance)
	rawTXT := make(map[string]string, len(entry.Text))
	for _, item := range entry.Text {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		rawTXT[strings.ToLower(key)] = value
	}

	name := normalizeName(instance, protocol)
	address := firstIPv4(entry.AddrIPv4)
	if address == "" {
		address = strings.TrimSuffix(entry.HostName, ".local.")
	}

	id := normalizeDeviceID(instance, address, rawTXT)
	modelName := firstNonEmpty(rawTXT["model"], rawTXT["am"])
	features := firstNonEmpty(rawTXT["features"], rawTXT["ft"])
	version := firstNonEmpty(rawTXT["srcvers"], rawTXT["vs"])
	isAudioTarget := protocol == "raop" || rawTXT["pk"] != "" || rawTXT["cn"] != ""
	isVideoCapable := protocol == "airplay"

	return model.Device{
		ID:              id,
		Name:            name,
		Address:         address,
		Port:            entry.Port,
		Model:           modelName,
		Features:        features,
		ProtocolVersion: version,
		IsAudioTarget:   isAudioTarget,
		IsVideoCapable:  isVideoCapable,
		RawTXT:          rawTXT,
		Protocols:       []string{protocol},
		Available:       true,
	}
}

func normalizeName(instance string, protocol string) string {
	if protocol == "raop" {
		if _, name, ok := strings.Cut(instance, "@"); ok {
			return strings.TrimSpace(name)
		}
	}
	return strings.TrimSpace(instance)
}

func unescapeInstance(instance string) string {
	replaced := strings.ReplaceAll(instance, `\ `, " ")
	unquoted, err := strconv.Unquote(`"` + replaced + `"`)
	if err != nil {
		return replaced
	}
	return unquoted
}

func normalizeDeviceID(instance string, address string, rawTXT map[string]string) string {
	if deviceID := strings.ToLower(rawTXT["deviceid"]); deviceID != "" {
		return deviceID
	}
	if prefix, _, ok := strings.Cut(instance, "@"); ok && prefix != "" {
		return strings.ToLower(prefix)
	}
	if address != "" {
		return strings.ToLower(address)
	}
	return strings.ToLower(strings.TrimSpace(instance))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstIPv4(ips []net.IP) string {
	sorted := make([]net.IP, 0, len(ips))
	sorted = append(sorted, ips...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].String() < sorted[j].String()
	})
	for _, ip := range sorted {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
	}
	if len(sorted) > 0 {
		return sorted[0].String()
	}
	return ""
}
