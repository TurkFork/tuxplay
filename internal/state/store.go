package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"tuxplay/internal/model"
)

var ErrDeviceNotFound = errors.New("device not found")

type persistedState struct {
	Devices []model.Device `json:"devices"`
	Routes  []model.Route  `json:"routes"`
	Groups  []model.Group  `json:"groups"`
}

type Store struct {
	mu            sync.RWMutex
	devices       map[string]model.Device
	routes        map[string]model.Route
	groups        map[string]model.Group
	pipewire      model.PipeWireStatus
	startedAt     time.Time
	socketPath    string
	statePath     string
	discoveryLive bool
}

func New(socketPath string, statePath string) (*Store, error) {
	s := &Store{
		devices:    make(map[string]model.Device),
		routes:     make(map[string]model.Route),
		groups:     make(map[string]model.Group),
		startedAt:  time.Now().UTC(),
		socketPath: socketPath,
		statePath:  statePath,
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

func StatePath() string {
	if explicit := os.Getenv("TUXPLAY_STATE"); explicit != "" {
		return explicit
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(os.TempDir(), "tuxplay-state.json")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "tuxplay", "state.json")
}

func (s *Store) load() error {
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := os.ReadFile(s.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read state: %w", err)
	}

	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}

	for _, device := range persisted.Devices {
		s.devices[device.ID] = device
	}
	for _, route := range persisted.Routes {
		s.routes[route.DeviceID] = route
	}
	for _, group := range persisted.Groups {
		s.groups[strings.ToLower(group.Name)] = group
	}
	s.normalizeDevicesLocked()
	return nil
}

func (s *Store) saveLocked() error {
	persisted := persistedState{
		Devices: make([]model.Device, 0, len(s.devices)),
		Routes:  make([]model.Route, 0, len(s.routes)),
		Groups:  make([]model.Group, 0, len(s.groups)),
	}

	for _, device := range s.devices {
		persisted.Devices = append(persisted.Devices, device)
	}
	for _, route := range s.routes {
		persisted.Routes = append(persisted.Routes, route)
	}
	for _, group := range s.groups {
		persisted.Groups = append(persisted.Groups, group)
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	tmpPath := s.statePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write state temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.statePath); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}

func (s *Store) SetDiscoveryLive(live bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discoveryLive = live
}

func (s *Store) SetPipeWireStatus(status model.PipeWireStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipewire = status
	s.reconcilePipeWireTargetsLocked()
}

func (s *Store) UpsertDevice(device model.Device) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	existingID := device.ID
	existing, ok := s.devices[existingID]
	if !ok {
		for id, candidate := range s.devices {
			if samePhysicalDevice(candidate, device) {
				existingID = id
				existing = candidate
				ok = true
				break
			}
		}
	}
	if ok {
		device = mergeDevice(existing, device, now)
	} else {
		device.LastSeen = now
		device.Available = true
	}

	delete(s.devices, device.ID)
	device.ID = existingID
	s.devices[device.ID] = device
	s.normalizeDevicesLocked()
	s.reconcilePipeWireTargetsLocked()
	_ = s.saveLocked()
}

func mergeDevice(existing model.Device, incoming model.Device, now time.Time) model.Device {
	merged := existing
	merged.LastSeen = now
	merged.Available = true

	if incoming.Name != "" {
		merged.Name = incoming.Name
	}
	if incoming.Address != "" {
		merged.Address = incoming.Address
	}
	if incoming.Port != 0 {
		merged.Port = incoming.Port
	}
	if incoming.Model != "" {
		merged.Model = incoming.Model
	}
	if incoming.Features != "" {
		merged.Features = incoming.Features
	}
	if incoming.ProtocolVersion != "" {
		merged.ProtocolVersion = incoming.ProtocolVersion
	}
	merged.IsAudioTarget = merged.IsAudioTarget || incoming.IsAudioTarget
	merged.IsVideoCapable = merged.IsVideoCapable || incoming.IsVideoCapable
	merged.Protocols = uniqueStrings(append(append([]string{}, existing.Protocols...), incoming.Protocols...))

	if merged.RawTXT == nil {
		merged.RawTXT = make(map[string]string)
	}
	for key, value := range incoming.RawTXT {
		merged.RawTXT[key] = value
	}

	return merged
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func (s *Store) Snapshot() model.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := model.Status{
		Devices:       make([]model.Device, 0, len(s.devices)),
		Routes:        make([]model.Route, 0, len(s.routes)),
		Groups:        make([]model.Group, 0, len(s.groups)),
		PipeWire:      s.pipewire,
		StartedAt:     s.startedAt,
		SocketPath:    s.socketPath,
		StatePath:     s.statePath,
		DiscoveryLive: s.discoveryLive,
	}

	for _, device := range s.devices {
		status.Devices = append(status.Devices, device)
	}
	for _, route := range s.routes {
		status.Routes = append(status.Routes, route)
	}
	for _, group := range s.groups {
		status.Groups = append(status.Groups, group)
	}

	sort.Slice(status.Devices, func(i, j int) bool {
		return strings.ToLower(status.Devices[i].Name) < strings.ToLower(status.Devices[j].Name)
	})
	sort.Slice(status.Routes, func(i, j int) bool {
		return strings.ToLower(status.Routes[i].DeviceName) < strings.ToLower(status.Routes[j].DeviceName)
	})
	sort.Slice(status.Groups, func(i, j int) bool {
		return strings.ToLower(status.Groups[i].Name) < strings.ToLower(status.Groups[j].Name)
	})

	return status
}

func (s *Store) Devices() []model.Device {
	return s.Snapshot().Devices
}

func (s *Store) Routes() []model.Route {
	return s.Snapshot().Routes
}

func (s *Store) ResolveDevice(name string) (model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	needle := strings.ToLower(strings.TrimSpace(name))
	if device, ok := s.devices[needle]; ok {
		return device, nil
	}

	var best model.Device
	bestScore := -1
	for _, device := range s.devices {
		if strings.ToLower(device.Name) != needle && strings.ToLower(device.ID) != needle {
			continue
		}
		score := resolveDeviceScore(device)
		if score > bestScore {
			best = device
			bestScore = score
		}
	}

	if bestScore >= 0 {
		return best, nil
	}

	return model.Device{}, ErrDeviceNotFound
}

func samePhysicalDevice(left model.Device, right model.Device) bool {
	if left.Address == "" || right.Address == "" {
		return false
	}
	return strings.EqualFold(left.Address, right.Address)
}

func resolveDeviceScore(device model.Device) int {
	score := 0
	if device.HasPipeWireSink {
		score += 100
	}
	if device.IsAudioTarget {
		score += 50
	}
	if slices.Contains(device.Protocols, "raop") {
		score += 25
	}
	if device.LastTransportBackend != "" {
		score += 10
	}
	return score
}

func (s *Store) normalizeDevicesLocked() {
	byAddress := make(map[string]string, len(s.devices))
	for id, device := range s.devices {
		key := strings.ToLower(strings.TrimSpace(device.Address))
		if key == "" {
			continue
		}

		existingID, ok := byAddress[key]
		if !ok {
			byAddress[key] = id
			continue
		}

		existing := s.devices[existingID]
		merged := mergeDevice(existing, device, maxTime(existing.LastSeen, device.LastSeen))
		merged.ID = preferredDeviceID(existingID, id)
		delete(s.devices, existingID)
		delete(s.devices, id)
		s.devices[merged.ID] = merged
		byAddress[key] = merged.ID
	}
}

func preferredDeviceID(left string, right string) string {
	switch {
	case strings.Count(left, ":") > strings.Count(right, ":"):
		return left
	case strings.Count(right, ":") > strings.Count(left, ":"):
		return right
	case len(left) <= len(right):
		return left
	default:
		return right
	}
}

func maxTime(left time.Time, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

func (s *Store) ReplaceRoutes(routes []model.Route) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.routes = make(map[string]model.Route, len(routes))
	for _, route := range routes {
		s.routes[route.DeviceID] = route
	}
	_ = s.saveLocked()
}

func (s *Store) SetRoute(route model.Route, add bool) model.Route {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !add {
		for id := range s.routes {
			if id != route.DeviceID {
				delete(s.routes, id)
			}
		}
	}
	route.UpdatedAt = time.Now().UTC()
	s.routes[route.DeviceID] = route
	_ = s.saveLocked()
	return route
}

func (s *Store) RemoveRoute(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.routes, deviceID)
	_ = s.saveLocked()
}

func (s *Store) UpdateRoute(deviceID string, mutate func(*model.Route)) (model.Route, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	route, ok := s.routes[deviceID]
	if !ok {
		return model.Route{}, ErrDeviceNotFound
	}

	mutate(&route)
	route.UpdatedAt = time.Now().UTC()
	s.routes[deviceID] = route
	_ = s.saveLocked()
	return route, nil
}

func (s *Store) UpsertGroup(name string, deviceIDs []string) model.Group {
	s.mu.Lock()
	defer s.mu.Unlock()

	group := model.Group{
		Name:      name,
		Devices:   slices.Clone(deviceIDs),
		UpdatedAt: time.Now().UTC(),
	}
	s.groups[strings.ToLower(name)] = group
	_ = s.saveLocked()
	return group
}

func (s *Store) AddGroupDevice(name string, deviceID string) (model.Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := strings.ToLower(name)
	group, ok := s.groups[key]
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}

	for _, existing := range group.Devices {
		if existing == deviceID {
			return group, nil
		}
	}

	group.Devices = append(group.Devices, deviceID)
	group.UpdatedAt = time.Now().UTC()
	s.groups[key] = group
	_ = s.saveLocked()
	return group, nil
}

func (s *Store) RemoveGroupDevice(name string, deviceID string) (model.Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := strings.ToLower(name)
	group, ok := s.groups[key]
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}

	filtered := group.Devices[:0]
	for _, existing := range group.Devices {
		if existing != deviceID {
			filtered = append(filtered, existing)
		}
	}
	group.Devices = slices.Clone(filtered)
	group.UpdatedAt = time.Now().UTC()
	s.groups[key] = group
	_ = s.saveLocked()
	return group, nil
}

func (s *Store) GetGroup(name string) (model.Group, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, ok := s.groups[strings.ToLower(name)]
	return group, ok
}

func (s *Store) reconcilePipeWireTargetsLocked() {
	for id, device := range s.devices {
		device.HasPipeWireSink = false
		device.PipeWireSinkName = ""
		device.PipeWireSinkID = 0
		device.PipeWireSinkInput = ""
		device.PipeWireSinkBackend = ""
		device.PipeWireSinkVolume = 0
		device.PipeWireSinkMuted = false

		for _, target := range s.pipewire.Targets {
			if matchesPipeWireTarget(device, target) {
				device.HasPipeWireSink = true
				device.PipeWireSinkName = target.Name
				device.PipeWireSinkID = target.ID
				device.PipeWireSinkInput = target.MonitorSource
				device.PipeWireSinkBackend = target.Backend
				device.PipeWireSinkVolume = target.Volume
				device.PipeWireSinkMuted = target.Muted
				device.LastTransportBackend = target.Backend
				break
			}
		}

		s.devices[id] = device
	}
}

func matchesPipeWireTarget(device model.Device, target model.PipeWireTarget) bool {
	deviceName := strings.ToLower(strings.TrimSpace(device.Name))
	deviceAddress := strings.ToLower(strings.TrimSpace(device.Address))
	targetName := strings.ToLower(strings.TrimSpace(target.Name))
	targetDesc := strings.ToLower(strings.TrimSpace(target.Description))
	targetAddress := strings.ToLower(strings.TrimSpace(target.Address))

	switch {
	case device.PipeWireSinkName != "" && strings.EqualFold(device.PipeWireSinkName, target.Name):
		return true
	case deviceAddress != "" && targetAddress != "" && deviceAddress == targetAddress:
		return true
	case deviceName != "" && (deviceName == targetDesc || strings.Contains(targetName, deviceName)):
		return true
	default:
		return false
	}
}
