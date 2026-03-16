package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tuxplay/internal/controller"
	"tuxplay/internal/discovery"
	"tuxplay/internal/group"
	"tuxplay/internal/model"
	"tuxplay/internal/pipewire"
	"tuxplay/internal/state"
)

type Server struct {
	store      *state.Store
	controller *controller.Service
	groups     *group.Service
	discovery  *discovery.Service
	pipewire   pipewire.Manager
	logger     *slog.Logger
	socketPath string
}

type routeRequest struct {
	Device string `json:"device"`
	Add    bool   `json:"add"`
}

type groupCreateRequest struct {
	Name    string   `json:"name"`
	Devices []string `json:"devices"`
}

type groupPlayRequest struct {
	Name string `json:"name"`
}

type groupMemberRequest struct {
	Name   string `json:"name"`
	Device string `json:"device"`
}

type deviceValueRequest struct {
	Device string `json:"device"`
}

type volumeRequest struct {
	Device  string `json:"device"`
	Percent int    `json:"percent"`
}

func New(socketPath string, logger *slog.Logger) (*Server, error) {
	store, err := state.New(socketPath, state.StatePath())
	if err != nil {
		return nil, err
	}
	store.ReplaceRoutes(nil)

	pipewireManager := pipewire.New(logger)
	controllerSvc := controller.New(store, pipewireManager, logger)

	return &Server{
		store:      store,
		controller: controllerSvc,
		groups:     group.New(store, controllerSvc, logger),
		discovery:  discovery.New(store, logger),
		pipewire:   pipewireManager,
		logger:     logger.With("component", "daemon"),
		socketPath: socketPath,
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	if err := removeStaleSocket(s.socketPath); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", s.handleStatus)
	mux.HandleFunc("/v1/devices", s.handleDevices)
	mux.HandleFunc("/v1/route", s.handleRoute)
	mux.HandleFunc("/v1/unroute", s.handleUnroute)
	mux.HandleFunc("/v1/volume", s.handleVolume)
	mux.HandleFunc("/v1/mute", s.handleMute)
	mux.HandleFunc("/v1/pause", s.handlePause)
	mux.HandleFunc("/v1/resume", s.handleResume)
	mux.HandleFunc("/v1/stop", s.handleStop)
	mux.HandleFunc("/v1/group/create", s.handleGroupCreate)
	mux.HandleFunc("/v1/group/play", s.handleGroupPlay)
	mux.HandleFunc("/v1/group/add", s.handleGroupAdd)
	mux.HandleFunc("/v1/group/remove", s.handleGroupRemove)

	server := &http.Server{Handler: mux}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on socket: %w", err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(s.socketPath)
	}()

	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := s.discovery.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("discovery exited", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.pipewire.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("pipewire exited", "error", err)
		}
	}()

	go s.syncPipeWire(ctx)

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	s.logger.Info("daemon listening", "socket", s.socketPath)
	err = server.Serve(ln)
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) syncPipeWire(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		if err := s.pipewire.Refresh(); err == nil {
			s.store.SetPipeWireStatus(s.pipewire.Status())
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func removeStaleSocket(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("path exists and is not a socket: %s", path)
		}
		return os.Remove(path)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Server) refreshPipeWireState() {
	if err := s.pipewire.Refresh(); err == nil {
		s.store.SetPipeWireStatus(s.pipewire.Status())
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.refreshPipeWireState()
	writeJSON(w, http.StatusOK, s.store.Snapshot())
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.refreshPipeWireState()
	writeJSON(w, http.StatusOK, map[string]any{"devices": s.store.Devices()})
}

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req routeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	route, err := s.controller.Route(req.Device, req.Add)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (s *Server) handleUnroute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req deviceValueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.controller.Unroute(req.Device); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVolume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req volumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	route, err := s.controller.SetVolume(req.Device, req.Percent)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (s *Server) handleMute(w http.ResponseWriter, r *http.Request) {
	s.handleRouteUpdate(w, r, func(device string) (model.Route, error) {
		return s.controller.Mute(device, true)
	})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.handleRouteUpdate(w, r, func(device string) (model.Route, error) {
		return s.controller.Pause(device, true)
	})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	s.handleRouteUpdate(w, r, func(device string) (model.Route, error) {
		return s.controller.Pause(device, false)
	})
}

func (s *Server) handleRouteUpdate(w http.ResponseWriter, r *http.Request, fn func(string) (model.Route, error)) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req deviceValueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	route, err := fn(req.Device)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req deviceValueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.controller.Stop(req.Device); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req groupCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	group, err := s.groups.Create(req.Name, req.Devices)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) handleGroupPlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req groupPlayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	routes, err := s.groups.Play(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": routes})
}

func (s *Server) handleGroupAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req groupMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	group, err := s.groups.Add(req.Name, req.Device)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) handleGroupRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req groupMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	group, err := s.groups.Remove(req.Name, req.Device)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{"error": message})
}

func SocketPath() string {
	if explicit := os.Getenv("TUXPLAY_SOCKET"); explicit != "" {
		return explicit
	}
	return filepath.Join(os.TempDir(), "tuxplay.sock")
}

func HTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

func DaemonReachable(socketPath string) bool {
	client := HTTPClient(socketPath)
	resp, err := client.Get("http://unix/v1/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func ParsePercent(value string) (int, error) {
	percent, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid percent: %w", err)
	}
	return percent, nil
}
