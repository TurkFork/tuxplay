package pipewire

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tuxplay/internal/model"
)

const (
	outputSinkName        = "tuxplay_output"
	outputSinkDisplayName = "TuxPlay"
	transportBackend      = "pipewire-raop"
)

var ErrTargetSinkUnavailable = fmt.Errorf("target PipeWire sink is unavailable")

type Manager interface {
	Start(context.Context) error
	Status() model.PipeWireStatus
	Refresh() error
	Route(model.Device) (model.Route, error)
	Unroute(model.Device) error
	SetVolume(model.Device, int) error
	SetMute(model.Device, bool) error
	Pause(model.Device) (model.Route, error)
	Resume(model.Device) (model.Route, error)
}

type Sink struct {
	Index         uint32            `json:"index"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	MonitorSource string            `json:"monitor_source"`
	Mute          bool              `json:"mute"`
	Properties    map[string]string `json:"properties"`
	Volume        map[string]struct {
		ValuePercent string `json:"value_percent"`
	} `json:"volume"`
}

type pulseModule struct {
	Index      uint32            `json:"index"`
	Name       string            `json:"name"`
	Argument   string            `json:"argument"`
	Properties map[string]string `json:"properties"`
}

type shortModule struct {
	ID       uint32
	Name     string
	Argument string
}

type PulseManager struct {
	logger              *slog.Logger
	opMu                sync.Mutex
	mu                  sync.RWMutex
	status              model.PipeWireStatus
	outputModuleID      uint32
	createdOutputSink   bool
	routeModules        map[string]uint32
	routeSinks          map[string]string
}

func New(logger *slog.Logger) *PulseManager {
	return &PulseManager{
		logger: logger.With("component", "pipewire"),
		status: model.PipeWireStatus{
			SinkName:         outputSinkName,
			SinkDescription:  outputSinkDisplayName,
			Backend:          transportBackend,
			ActiveStreams:    []string{},
			Targets:          []model.PipeWireTarget{},
			OutputSinkExists: false,
		},
		routeModules: make(map[string]uint32),
		routeSinks:   make(map[string]string),
	}
}

func (m *PulseManager) Start(ctx context.Context) error {
	m.opMu.Lock()
	if err := m.ensureOutputSink(); err != nil {
		m.opMu.Unlock()
		return err
	}
	if err := m.refreshLocked(true); err != nil {
		m.opMu.Unlock()
		return err
	}
	m.opMu.Unlock()

	m.logger.Info("starting PipeWire manager", "backend", transportBackend, "sink", outputSinkName)
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.cleanup()
			m.logger.Info("stopping PipeWire manager")
			return nil
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				m.logger.Error("refresh failed", "error", err)
			}
		}
	}
}

func (m *PulseManager) Status() model.PipeWireStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *PulseManager) Refresh() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	return m.refreshLocked(true)
}

func (m *PulseManager) refreshLocked(ensure bool) error {
	sinks, err := m.listSinks()
	if err != nil {
		return err
	}

	status := model.PipeWireStatus{
		SinkName:        outputSinkName,
		SinkDescription: outputSinkDisplayName,
		Backend:         transportBackend,
		Targets:         make([]model.PipeWireTarget, 0),
		ActiveStreams:   make([]string, 0),
	}

	for _, sink := range sinks {
		if sink.Name == outputSinkName {
			status.OutputSinkExists = true
			status.SinkID = sink.Index
			status.SourceName = sink.MonitorSource
			status.SinkDescription = firstNonEmpty(sink.Description, outputSinkDisplayName)
			continue
		}

		if !strings.HasPrefix(sink.Name, "raop_sink.") {
			continue
		}

		target := model.PipeWireTarget{
			ID:            sink.Index,
			Name:          sink.Name,
			Description:   firstNonEmpty(sink.Description, sink.Name),
			MonitorSource: sink.MonitorSource,
			Backend:       transportBackend,
			Address:       firstNonEmpty(sink.Properties["raop.ip"], parseAddressFromSinkName(sink.Name)),
			Audio:         true,
			Available:     true,
			Volume:        parseVolumePercent(sink.Volume),
			Muted:         sink.Mute,
		}
		status.Targets = append(status.Targets, target)
	}

	sort.Slice(status.Targets, func(i, j int) bool {
		return strings.ToLower(status.Targets[i].Description) < strings.ToLower(status.Targets[j].Description)
	})

	m.mu.Lock()
	status.OutputModuleID = m.outputModuleID
	for deviceID, moduleID := range m.routeModules {
		status.ActiveStreams = append(status.ActiveStreams, fmt.Sprintf("%s:%d", deviceID, moduleID))
	}
	sort.Strings(status.ActiveStreams)
	m.status = status
	m.mu.Unlock()

	if ensure && !status.OutputSinkExists {
		return m.ensureOutputSink()
	}

	return nil
}

func (m *PulseManager) Route(device model.Device) (model.Route, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	if err := m.refreshLocked(true); err != nil {
		return model.Route{}, err
	}

	target, err := m.resolveTarget(device)
	if err != nil {
		return model.Route{}, err
	}

	m.mu.RLock()
	existingModuleID := m.routeModules[device.ID]
	existingSink := m.routeSinks[device.ID]
	m.mu.RUnlock()
	if existingModuleID != 0 && existingSink == target.Name {
		return model.Route{
			DeviceID:         device.ID,
			DeviceName:       device.Name,
			Volume:           target.Volume,
			Muted:            target.Muted,
			Status:           "routed",
			TransportBackend: transportBackend,
			PipeWireSinkName: target.Name,
			LoopbackModuleID: existingModuleID,
			UpdatedAt:        time.Now().UTC(),
		}, nil
	}

	if existingModuleID != 0 {
		if err := unloadModule(existingModuleID); err != nil {
			m.logger.Warn("failed to unload stale loopback", "module", existingModuleID, "error", err)
		}
	}
	if err := m.cleanupLoopbacksForTarget(target.Name); err != nil {
		return model.Route{}, err
	}

	moduleID, err := m.loadLoopback(target.Name)
	if err != nil {
		return model.Route{}, err
	}

	m.mu.Lock()
	m.routeModules[device.ID] = moduleID
	m.routeSinks[device.ID] = target.Name
	m.mu.Unlock()

	return model.Route{
		DeviceID:         device.ID,
		DeviceName:       device.Name,
		Volume:           target.Volume,
		Muted:            target.Muted,
		Status:           "routed",
		TransportBackend: transportBackend,
		PipeWireSinkName: target.Name,
		LoopbackModuleID: moduleID,
		UpdatedAt:        time.Now().UTC(),
	}, nil
}

func (m *PulseManager) Unroute(device model.Device) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	moduleID := m.routeModules[device.ID]
	delete(m.routeModules, device.ID)
	delete(m.routeSinks, device.ID)
	m.mu.Unlock()

	target, err := m.resolveTarget(device)
	if err != nil && moduleID == 0 {
		return nil
	}
	if moduleID != 0 {
		if err := unloadModule(moduleID); err != nil {
			return err
		}
	}
	if err == nil {
		return m.cleanupLoopbacksForTarget(target.Name)
	}
	return nil
}

func (m *PulseManager) SetVolume(device model.Device, percent int) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	target, err := m.resolveTarget(device)
	if err != nil {
		return err
	}
	_, err = runPactl("set-sink-volume", target.Name, fmt.Sprintf("%d%%", percent))
	return err
}

func (m *PulseManager) SetMute(device model.Device, muted bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	target, err := m.resolveTarget(device)
	if err != nil {
		return err
	}
	value := "0"
	if muted {
		value = "1"
	}
	_, err = runPactl("set-sink-mute", target.Name, value)
	return err
}

func (m *PulseManager) Pause(device model.Device) (model.Route, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	moduleID := m.routeModules[device.ID]
	delete(m.routeModules, device.ID)
	delete(m.routeSinks, device.ID)
	m.mu.Unlock()
	if moduleID != 0 {
		if err := unloadModule(moduleID); err != nil {
			return model.Route{}, err
		}
	}

	target, err := m.resolveTarget(device)
	if err != nil {
		return model.Route{}, err
	}

	return model.Route{
		DeviceID:         device.ID,
		DeviceName:       device.Name,
		Volume:           target.Volume,
		Muted:            target.Muted,
		Paused:           true,
		Status:           "paused",
		TransportBackend: transportBackend,
		PipeWireSinkName: target.Name,
		UpdatedAt:        time.Now().UTC(),
	}, nil
}

func (m *PulseManager) Resume(device model.Device) (model.Route, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	if err := m.refreshLocked(true); err != nil {
		return model.Route{}, err
	}
	target, err := m.resolveTarget(device)
	if err != nil {
		return model.Route{}, err
	}

	moduleID, err := m.loadLoopback(target.Name)
	if err != nil {
		return model.Route{}, err
	}
	m.mu.Lock()
	m.routeModules[device.ID] = moduleID
	m.routeSinks[device.ID] = target.Name
	m.mu.Unlock()

	return model.Route{
		DeviceID:         device.ID,
		DeviceName:       device.Name,
		Volume:           target.Volume,
		Muted:            target.Muted,
		Paused:           false,
		Status:           "routed",
		TransportBackend: transportBackend,
		PipeWireSinkName: target.Name,
		LoopbackModuleID: moduleID,
		UpdatedAt:        time.Now().UTC(),
	}, nil
}

func (m *PulseManager) cleanup() {
	m.mu.Lock()
	routeModules := make(map[string]uint32, len(m.routeModules))
	for key, value := range m.routeModules {
		routeModules[key] = value
	}
	outputModuleID := m.outputModuleID
	createdOutputSink := m.createdOutputSink
	m.routeModules = make(map[string]uint32)
	m.routeSinks = make(map[string]string)
	m.mu.Unlock()

	for _, moduleID := range routeModules {
		if moduleID != 0 {
			if err := unloadModule(moduleID); err != nil {
				m.logger.Warn("failed to unload loopback module", "module", moduleID, "error", err)
			}
		}
	}
	if createdOutputSink && outputModuleID != 0 {
		if err := unloadModule(outputModuleID); err != nil {
			m.logger.Warn("failed to unload output sink module", "module", outputModuleID, "error", err)
		}
	}
}

func (m *PulseManager) ensureOutputSink() error {
	if err := m.refreshWithoutEnsureLocked(); err != nil {
		return err
	}

	m.mu.RLock()
	exists := m.status.OutputSinkExists
	m.mu.RUnlock()
	if exists {
		return nil
	}

	output, err := runPactl(
		"load-module",
		"module-null-sink",
		"sink_name="+outputSinkName,
		"sink_properties=device.description="+outputSinkDisplayName,
		"format=s16le",
		"rate=44100",
		"channels=2",
	)
	if err != nil {
		return err
	}

	moduleID64, err := strconv.ParseUint(strings.TrimSpace(output), 10, 32)
	if err != nil {
		return fmt.Errorf("parse null sink module id: %w", err)
	}

	m.mu.Lock()
	m.outputModuleID = uint32(moduleID64)
	m.createdOutputSink = true
	m.mu.Unlock()

	return m.refreshWithoutEnsureLocked()
}

func (m *PulseManager) RefreshWithoutEnsure() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	return m.refreshWithoutEnsureLocked()
}

func (m *PulseManager) refreshWithoutEnsureLocked() error {
	sinks, err := m.listSinks()
	if err != nil {
		return err
	}

	modules, err := m.listShortModules()
	if err != nil {
		return err
	}

	status := model.PipeWireStatus{
		SinkName:        outputSinkName,
		SinkDescription: outputSinkDisplayName,
		Backend:         transportBackend,
		Targets:         make([]model.PipeWireTarget, 0),
		ActiveStreams:   make([]string, 0),
	}

	outputSinkCount := 0
	extraOutputModules := make([]uint32, 0)
	for _, sink := range sinks {
		if sink.Name == outputSinkName {
			outputSinkCount++
			if !status.OutputSinkExists {
				status.OutputSinkExists = true
				status.SinkID = sink.Index
				status.SourceName = sink.MonitorSource
				status.SinkDescription = firstNonEmpty(sink.Description, outputSinkDisplayName)
				if pulseModuleID := sink.Properties["pulse.module.id"]; pulseModuleID != "" {
					if value, err := strconv.ParseUint(pulseModuleID, 10, 32); err == nil {
						status.OutputModuleID = uint32(value)
					}
				}
			} else if pulseModuleID := sink.Properties["pulse.module.id"]; pulseModuleID != "" {
				if value, err := strconv.ParseUint(pulseModuleID, 10, 32); err == nil {
					extraOutputModules = append(extraOutputModules, uint32(value))
				}
			}
			continue
		}

		if !strings.HasPrefix(sink.Name, "raop_sink.") {
			continue
		}

		target := model.PipeWireTarget{
			ID:            sink.Index,
			Name:          sink.Name,
			Description:   firstNonEmpty(sink.Description, sink.Name),
			MonitorSource: sink.MonitorSource,
			Backend:       transportBackend,
			Address:       firstNonEmpty(sink.Properties["raop.ip"], parseAddressFromSinkName(sink.Name)),
			Audio:         true,
			Available:     true,
			Volume:        parseVolumePercent(sink.Volume),
			Muted:         sink.Mute,
		}
		status.Targets = append(status.Targets, target)
	}

	sort.Slice(status.Targets, func(i, j int) bool {
		return strings.ToLower(status.Targets[i].Description) < strings.ToLower(status.Targets[j].Description)
	})

	m.mu.Lock()
	if status.OutputModuleID != 0 {
		m.outputModuleID = status.OutputModuleID
	}
	for deviceID, moduleID := range m.routeModules {
		status.ActiveStreams = append(status.ActiveStreams, fmt.Sprintf("%s:%d", deviceID, moduleID))
	}
	sort.Strings(status.ActiveStreams)
	status.OutputModuleID = m.outputModuleID
	m.status = status
	knownRouteModules := make(map[uint32]struct{}, len(m.routeModules))
	for _, moduleID := range m.routeModules {
		knownRouteModules[moduleID] = struct{}{}
	}
	m.mu.Unlock()

	for _, moduleID := range extraOutputModules {
		if moduleID != 0 && moduleID != status.OutputModuleID {
			if err := unloadModule(moduleID); err != nil {
				m.logger.Warn("failed to unload duplicate output sink", "module", moduleID, "error", err)
			}
		}
	}
	for _, module := range modules {
		if module.Name != "module-loopback" || !isTuxPlayLoopback(module.Argument) {
			continue
		}
		if _, ok := knownRouteModules[module.ID]; ok {
			continue
		}
		if err := unloadModule(module.ID); err != nil {
			m.logger.Warn("failed to unload stale loopback", "module", module.ID, "error", err)
		}
	}
	return nil
}

func (m *PulseManager) resolveTarget(device model.Device) (model.PipeWireTarget, error) {
	m.mu.RLock()
	status := m.status
	m.mu.RUnlock()

	for _, target := range status.Targets {
		if device.PipeWireSinkName != "" && strings.EqualFold(device.PipeWireSinkName, target.Name) {
			return target, nil
		}
		if device.Address != "" && target.Address != "" && device.Address == target.Address {
			return target, nil
		}
		if strings.EqualFold(device.Name, target.Description) || strings.Contains(strings.ToLower(target.Name), strings.ToLower(device.Name)) {
			return target, nil
		}
	}

	return model.PipeWireTarget{}, ErrTargetSinkUnavailable
}

func (m *PulseManager) loadLoopback(targetSink string) (uint32, error) {
	output, err := runPactl(
		"load-module",
		"module-loopback",
		"source="+outputSinkName+".monitor",
		"sink="+targetSink,
		"latency_msec=250",
		"source_dont_move=true",
		"sink_dont_move=true",
	)
	if err != nil {
		return 0, err
	}

	moduleID64, err := strconv.ParseUint(strings.TrimSpace(output), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse loopback module id: %w", err)
	}
	return uint32(moduleID64), nil
}

func (m *PulseManager) listSinks() ([]Sink, error) {
	output, err := runPactl("--format=json", "list", "sinks")
	if err != nil {
		return nil, err
	}

	var sinks []Sink
	if err := json.Unmarshal([]byte(output), &sinks); err != nil {
		return nil, fmt.Errorf("decode sinks JSON: %w", err)
	}
	return sinks, nil
}

func (m *PulseManager) listModules() ([]pulseModule, error) {
	output, err := runPactl("--format=json", "list", "modules")
	if err != nil {
		return nil, err
	}

	var modules []pulseModule
	if err := json.Unmarshal([]byte(output), &modules); err != nil {
		return nil, fmt.Errorf("decode modules JSON: %w", err)
	}
	return modules, nil
}

func (m *PulseManager) listShortModules() ([]shortModule, error) {
	output, err := runPactl("list", "short", "modules")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	modules := make([]shortModule, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		id, err := strconv.ParseUint(parts[0], 10, 32)
		if err != nil {
			continue
		}
		module := shortModule{
			ID:   uint32(id),
			Name: parts[1],
		}
		if len(parts) == 3 {
			module.Argument = parts[2]
		}
		modules = append(modules, module)
	}
	return modules, nil
}

func (m *PulseManager) cleanupLoopbacksForTarget(targetSink string) error {
	modules, err := m.listShortModules()
	if err != nil {
		return err
	}
	for _, module := range modules {
		if module.Name != "module-loopback" || !isTuxPlayLoopback(module.Argument) {
			continue
		}
		if !strings.Contains(module.Argument, "sink="+targetSink) {
			continue
		}
		if err := unloadModule(module.ID); err != nil {
			return err
		}
	}
	return nil
}

func isTuxPlayLoopback(argument string) bool {
	return strings.Contains(argument, "source="+outputSinkName+".monitor")
}

func parseVolumePercent(volume map[string]struct{ ValuePercent string `json:"value_percent"` }) int {
	for _, channel := range volume {
		value := strings.TrimSuffix(channel.ValuePercent, "%")
		percent, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return percent
		}
	}
	return 100
}

func parseAddressFromSinkName(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) < 5 {
		return ""
	}
	for i := 0; i+3 < len(parts); i++ {
		candidate := strings.Join(parts[i:i+4], ".")
		if isIPv4(candidate) {
			return candidate
		}
	}
	return ""
}

func isIPv4(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > 255 {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func unloadModule(moduleID uint32) error {
	if moduleID == 0 {
		return nil
	}
	_, err := runPactl("unload-module", strconv.FormatUint(uint64(moduleID), 10))
	return err
}

func runPactl(args ...string) (string, error) {
	cmd := exec.Command("pactl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pactl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
