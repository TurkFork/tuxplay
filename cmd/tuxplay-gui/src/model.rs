use serde::Deserialize;

#[derive(Clone, Debug, Deserialize)]
pub struct Device {
    pub id: String,
    pub name: String,
    pub address: String,
    pub model: String,
    pub protocols: Vec<String>,
    pub has_pipewire_sink: bool,
    pub pipewire_sink_name: String,
    pub pipewire_sink_backend: String,
    pub pipewire_sink_volume: i32,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Route {
    pub device_id: String,
    pub device_name: String,
    pub volume: i32,
    pub muted: bool,
    pub paused: bool,
    pub status: String,
    pub transport_backend: String,
    pub pipewire_sink_name: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Group {
    pub name: String,
    pub devices: Vec<String>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct PipeWireTarget {
    pub name: String,
    pub description: String,
    pub address: String,
    pub volume: i32,
    pub muted: bool,
}

#[derive(Clone, Debug, Deserialize)]
pub struct PipeWireStatus {
    pub sink_name: String,
    pub backend: String,
    pub output_sink_exists: bool,
    pub targets: Vec<PipeWireTarget>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Status {
    pub devices: Vec<Device>,
    pub routes: Vec<Route>,
    pub groups: Vec<Group>,
    pub pipewire: PipeWireStatus,
    pub socket_path: String,
    pub state_path: String,
    pub discovery_live: bool,
}
