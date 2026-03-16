package controller

import (
	"fmt"
	"log/slog"

	"tuxplay/internal/model"
	"tuxplay/internal/pipewire"
	"tuxplay/internal/state"
)

type Service struct {
	store    *state.Store
	pipewire pipewire.Manager
	logger   *slog.Logger
}

func New(store *state.Store, pipewireManager pipewire.Manager, logger *slog.Logger) *Service {
	return &Service{
		store:    store,
		pipewire: pipewireManager,
		logger:   logger.With("component", "controller"),
	}
}

func (s *Service) Route(deviceName string, add bool) (model.Route, error) {
	device, err := s.store.ResolveDevice(deviceName)
	if err != nil {
		return model.Route{}, err
	}
	if !device.HasPipeWireSink {
		return model.Route{}, fmt.Errorf("device %q does not have a mapped PipeWire RAOP sink yet", device.Name)
	}

	if !add {
		for _, existing := range s.store.Routes() {
			if existing.DeviceID == device.ID {
				continue
			}
			existingDevice, err := s.store.ResolveDevice(existing.DeviceID)
			if err == nil {
				_ = s.pipewire.Unroute(existingDevice)
			}
			s.store.RemoveRoute(existing.DeviceID)
		}
	}

	route, err := s.pipewire.Route(device)
	if err != nil {
		return model.Route{}, err
	}

	if route.Volume == 0 {
		route.Volume = 100
	}
	route = s.store.SetRoute(route, true)
	s.logger.Info("route updated", "device", device.Name, "mode", ternary(add, "add", "set"))
	return route, nil
}

func (s *Service) Unroute(deviceName string) error {
	device, err := s.store.ResolveDevice(deviceName)
	if err != nil {
		return err
	}
	if err := s.pipewire.Unroute(device); err != nil {
		return err
	}
	s.store.RemoveRoute(device.ID)
	s.logger.Info("route removed", "device", device.Name)
	return nil
}

func (s *Service) SetVolume(deviceName string, percent int) (model.Route, error) {
	if percent < 0 || percent > 100 {
		return model.Route{}, fmt.Errorf("volume must be between 0 and 100")
	}
	device, err := s.store.ResolveDevice(deviceName)
	if err != nil {
		return model.Route{}, err
	}
	if err := s.pipewire.SetVolume(device, percent); err != nil {
		return model.Route{}, err
	}

	route, err := s.store.UpdateRoute(device.ID, func(route *model.Route) {
		route.Volume = percent
		route.Status = "volume-updated"
	})
	if err != nil {
		return model.Route{}, err
	}
	s.logger.Info("volume updated", "device", device.Name, "volume", percent)
	return route, nil
}

func (s *Service) Mute(deviceName string, muted bool) (model.Route, error) {
	device, err := s.store.ResolveDevice(deviceName)
	if err != nil {
		return model.Route{}, err
	}
	if err := s.pipewire.SetMute(device, muted); err != nil {
		return model.Route{}, err
	}

	route, err := s.store.UpdateRoute(device.ID, func(route *model.Route) {
		route.Muted = muted
		if muted {
			route.Status = "muted"
			return
		}
		route.Status = "volume-updated"
	})
	if err != nil {
		return model.Route{}, err
	}
	s.logger.Info("mute updated", "device", device.Name, "muted", muted)
	return route, nil
}

func (s *Service) Pause(deviceName string, paused bool) (model.Route, error) {
	device, err := s.store.ResolveDevice(deviceName)
	if err != nil {
		return model.Route{}, err
	}

	var route model.Route
	if paused {
		route, err = s.pipewire.Pause(device)
	} else {
		route, err = s.pipewire.Resume(device)
	}
	if err != nil {
		return model.Route{}, err
	}

	route, err = s.store.UpdateRoute(device.ID, func(existing *model.Route) {
		existing.Paused = paused
		existing.Status = route.Status
		existing.LoopbackModuleID = route.LoopbackModuleID
		existing.PipeWireSinkName = route.PipeWireSinkName
	})
	if err != nil {
		if !paused {
			route = s.store.SetRoute(route, true)
			return route, nil
		}
		return model.Route{}, err
	}

	s.logger.Info("pause updated", "device", device.Name, "paused", paused)
	return route, nil
}

func (s *Service) Stop(deviceName string) error {
	return s.Unroute(deviceName)
}

func ternary[T any](cond bool, left, right T) T {
	if cond {
		return left
	}
	return right
}
