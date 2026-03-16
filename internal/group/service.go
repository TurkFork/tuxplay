package group

import (
	"fmt"
	"log/slog"

	"tuxplay/internal/controller"
	"tuxplay/internal/model"
	"tuxplay/internal/state"
)

type Service struct {
	store      *state.Store
	controller *controller.Service
	logger     *slog.Logger
}

func New(store *state.Store, controller *controller.Service, logger *slog.Logger) *Service {
	return &Service{
		store:      store,
		controller: controller,
		logger:     logger.With("component", "group"),
	}
}

func (s *Service) Create(name string, deviceNames []string) (model.Group, error) {
	if name == "" {
		return model.Group{}, fmt.Errorf("group name is required")
	}
	if len(deviceNames) == 0 {
		return model.Group{}, fmt.Errorf("group requires at least one device")
	}

	deviceIDs := make([]string, 0, len(deviceNames))
	for _, deviceName := range deviceNames {
		device, err := s.store.ResolveDevice(deviceName)
		if err != nil {
			return model.Group{}, err
		}
		deviceIDs = append(deviceIDs, device.ID)
	}

	group := s.store.UpsertGroup(name, deviceIDs)
	s.logger.Info("group saved", "name", name, "devices", len(deviceIDs))
	return group, nil
}

func (s *Service) Add(name string, deviceName string) (model.Group, error) {
	device, err := s.store.ResolveDevice(deviceName)
	if err != nil {
		return model.Group{}, err
	}
	group, err := s.store.AddGroupDevice(name, device.ID)
	if err != nil {
		return model.Group{}, err
	}
	s.logger.Info("group device added", "group", name, "device", device.Name)
	return group, nil
}

func (s *Service) Remove(name string, deviceName string) (model.Group, error) {
	device, err := s.store.ResolveDevice(deviceName)
	if err != nil {
		return model.Group{}, err
	}
	group, err := s.store.RemoveGroupDevice(name, device.ID)
	if err != nil {
		return model.Group{}, err
	}
	s.logger.Info("group device removed", "group", name, "device", device.Name)
	return group, nil
}

func (s *Service) Play(name string) ([]model.Route, error) {
	group, ok := s.store.GetGroup(name)
	if !ok {
		return nil, fmt.Errorf("group not found")
	}

	routes := make([]model.Route, 0, len(group.Devices))
	for _, deviceID := range group.Devices {
		device, err := s.store.ResolveDevice(deviceID)
		if err != nil {
			return nil, err
		}
		route, err := s.controller.Route(device.ID, true)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}

	s.logger.Info("group routed", "name", name, "devices", len(routes))
	return routes, nil
}
