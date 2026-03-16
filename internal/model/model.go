package model

import "time"

type Device struct {
	ID                    string            `json:"id"`
	Name                  string            `json:"name"`
	Address               string            `json:"address"`
	Port                  int               `json:"port"`
	Model                 string            `json:"model"`
	Features              string            `json:"features"`
	ProtocolVersion       string            `json:"protocol_version"`
	IsAudioTarget         bool              `json:"is_audio_target"`
	IsVideoCapable        bool              `json:"is_video_capable"`
	LastSeen              time.Time         `json:"last_seen"`
	RawTXT                map[string]string `json:"raw_txt"`
	Protocols             []string          `json:"protocols"`
	Available             bool              `json:"available"`
	HasPipeWireSink       bool              `json:"has_pipewire_sink"`
	PipeWireSinkName      string            `json:"pipewire_sink_name"`
	PipeWireSinkID        uint32            `json:"pipewire_sink_id"`
	PipeWireSinkInput     string            `json:"pipewire_sink_input"`
	PipeWireSinkBackend   string            `json:"pipewire_sink_backend"`
	PipeWireSinkVolume    int               `json:"pipewire_sink_volume"`
	PipeWireSinkMuted     bool              `json:"pipewire_sink_muted"`
	LastTransportBackend  string            `json:"last_transport_backend"`
}

type Route struct {
	DeviceID          string    `json:"device_id"`
	DeviceName        string    `json:"device_name"`
	Muted             bool      `json:"muted"`
	Volume            int       `json:"volume"`
	Paused            bool      `json:"paused"`
	Status            string    `json:"status"`
	UpdatedAt         time.Time `json:"updated_at"`
	TransportBackend  string    `json:"transport_backend"`
	PipeWireSinkName  string    `json:"pipewire_sink_name"`
	LoopbackModuleID  uint32    `json:"loopback_module_id"`
}

type Group struct {
	Name      string    `json:"name"`
	Devices   []string  `json:"devices"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PipeWireTarget struct {
	ID            uint32 `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	MonitorSource string `json:"monitor_source"`
	Backend       string `json:"backend"`
	Address       string `json:"address"`
	Audio         bool   `json:"audio"`
	Available     bool   `json:"available"`
	Volume        int    `json:"volume"`
	Muted         bool   `json:"muted"`
}

type PipeWireStatus struct {
	SinkName         string           `json:"sink_name"`
	SinkDescription  string           `json:"sink_description"`
	SinkID           uint32           `json:"sink_id"`
	SourceName       string           `json:"source_name"`
	Backend          string           `json:"backend"`
	OutputSinkExists bool             `json:"output_sink_exists"`
	OutputModuleID   uint32           `json:"output_module_id"`
	ActiveStreams    []string         `json:"active_streams"`
	Targets          []PipeWireTarget `json:"targets"`
}

type Status struct {
	Devices       []Device       `json:"devices"`
	Routes        []Route        `json:"routes"`
	Groups        []Group        `json:"groups"`
	PipeWire      PipeWireStatus `json:"pipewire"`
	StartedAt     time.Time      `json:"started_at"`
	SocketPath    string         `json:"socket_path"`
	StatePath     string         `json:"state_path"`
	DiscoveryLive bool           `json:"discovery_live"`
}
