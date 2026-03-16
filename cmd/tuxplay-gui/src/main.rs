mod api;
mod model;

use std::cell::RefCell;
use std::rc::Rc;
use std::sync::mpsc;
use std::thread;
use std::time::Duration;

use adw::prelude::*;
use anyhow::Result;
use glib::{self, ControlFlow};
use gtk::prelude::*;

use crate::api::Client;
use crate::model::{Device, Route, Status};

const SOCKET_PATH: &str = "/tmp/tuxplay.sock";

#[derive(Clone)]
struct Ui {
    root: gtk::Box,
    status_label: gtk::Label,
    refresh_button: gtk::Button,
    spinner: gtk::Spinner,
    last_status: Rc<RefCell<Option<Status>>>,
}

enum Message {
    Status(Status),
    Error(String),
}

fn main() {
    let app = adw::Application::builder()
        .application_id("dev.tuxplay.gui")
        .build();

    app.connect_activate(build_ui);
    app.run();
}

fn build_ui(app: &adw::Application) {
    let client = Client::new(SOCKET_PATH);
    let (sender, receiver) = mpsc::channel::<Message>();

    let header = adw::HeaderBar::new();
    let spinner = gtk::Spinner::new();
    let refresh_button = gtk::Button::builder()
        .icon_name("view-refresh-symbolic")
        .build();

    header.pack_start(&spinner);
    header.pack_end(&refresh_button);

    let body = gtk::Box::new(gtk::Orientation::Vertical, 0);
    let scrolled = gtk::ScrolledWindow::builder()
        .hscrollbar_policy(gtk::PolicyType::Never)
        .vexpand(true)
        .hexpand(true)
        .build();

    let content = gtk::Box::new(gtk::Orientation::Vertical, 24);
    content.set_margin_top(24);
    content.set_margin_bottom(24);
    content.set_margin_start(24);
    content.set_margin_end(24);
    scrolled.set_child(Some(&content));

    let status_label = gtk::Label::new(None);
    status_label.set_xalign(0.0);
    status_label.add_css_class("dim-label");
    status_label.set_margin_top(6);
    status_label.set_margin_bottom(6);
    status_label.set_margin_start(12);
    status_label.set_margin_end(12);

    body.append(&header);
    body.append(&scrolled);
    body.append(&status_label);

    let window = adw::ApplicationWindow::builder()
        .application(app)
        .title("TuxPlay GUI")
        .default_width(960)
        .default_height(760)
        .content(&body)
        .build();

    let ui = Ui {
        root: content,
        status_label,
        refresh_button,
        spinner,
        last_status: Rc::new(RefCell::new(None)),
    };

    {
        let ui = ui.clone();
        let sender = sender.clone();
        let client = client.clone();
        ui.refresh_button.connect_clicked(move |_| {
            request_status(client.clone(), sender.clone());
        });
    }

    {
        let ui = ui.clone();
        let sender = sender.clone();
        let client = client.clone();
        glib::timeout_add_seconds_local(5, move || {
            request_status(client.clone(), sender.clone());
            ControlFlow::Continue
        });
    }

    {
        let ui = ui.clone();
        let sender = sender.clone();
        let client = client.clone();
        glib::timeout_add_local(Duration::from_millis(150), move || {
            while let Ok(message) = receiver.try_recv() {
                ui.spinner.stop();
                ui.refresh_button.set_sensitive(true);
                match message {
                    Message::Status(status) => {
                        *ui.last_status.borrow_mut() = Some(status.clone());
                        render_status(&ui, &client, sender.clone(), &status);
                    }
                    Message::Error(error) => render_error(&ui, &error),
                }
            }
            ControlFlow::Continue
        });
    }

    request_status(client, sender);
    window.present();
}

fn request_status(client: Client, sender: mpsc::Sender<Message>) {
    thread::spawn(move || {
        let message = match client.status() {
            Ok(status) => Message::Status(status),
            Err(error) => Message::Error(error.to_string()),
        };
        let _ = sender.send(message);
    });
}

fn render_status(ui: &Ui, client: &Client, sender: mpsc::Sender<Message>, status: &Status) {
    clear_box(&ui.root);

    ui.root.append(&section_title("Overview"));
    ui.root.append(&info_row("Daemon", &status.socket_path));
    ui.root.append(&info_row("State", &status.state_path));
    ui.root.append(&info_row(
        "Output Sink",
        &format!(
            "{} ({})",
            status.pipewire.sink_name,
            if status.pipewire.output_sink_exists {
                "present"
            } else {
                "missing"
            }
        ),
    ));
    ui.root.append(&info_row("Transport", &status.pipewire.backend));

    if !status.routes.is_empty() {
        ui.root.append(&section_title("Active Targets"));
        for route in &status.routes {
            ui.root.append(&info_row(
                &route.device_name,
                &format!(
                    "{} • {}% • muted={} • paused={} • {}",
                    route.pipewire_sink_name,
                    route.volume,
                    route.muted,
                    route.paused,
                    route.status
                ),
            ));
        }
    }

    if !status.groups.is_empty() {
        ui.root.append(&section_title("Groups"));
        for group in &status.groups {
            let members = group
                .devices
                .iter()
                .map(|id| device_name_for(status, id))
                .collect::<Vec<_>>()
                .join(", ");
            ui.root.append(&info_row(&group.name, &members));
        }
    }

    ui.root.append(&section_title("PipeWire Targets"));
    if status.pipewire.targets.is_empty() {
        ui.root.append(&info_row("Targets", "No mapped PipeWire RAOP sinks"));
    } else {
        for target in &status.pipewire.targets {
            ui.root.append(&info_row(
                &target.description,
                &format!(
                    "{} • {} • {}% • muted={}",
                    target.name, target.address, target.volume, target.muted
                ),
            ));
        }
    }

    ui.root.append(&section_title("Devices"));
    for device in &status.devices {
        ui.root.append(&device_row(status, client, sender.clone(), device));
    }

    ui.status_label.set_label(&format!(
        "Discovery: {}   Devices: {}   Targets: {}   Routes: {}",
        status.discovery_live,
        status.devices.len(),
        status.pipewire.targets.len(),
        status.routes.len()
    ));
}

fn render_error(ui: &Ui, error: &str) {
    clear_box(&ui.root);
    ui.root.append(&section_title("Daemon Error"));
    ui.root
        .append(&info_row("Socket", SOCKET_PATH));
    ui.root.append(&info_row("Error", error));
    ui.status_label
        .set_label("Unable to reach the TuxPlay daemon over /tmp/tuxplay.sock");
}

fn device_row(
    status: &Status,
    client: &Client,
    sender: mpsc::Sender<Message>,
    device: &Device,
) -> adw::ActionRow {
    let row = adw::ActionRow::builder().title(&device.name).build();
    row.set_subtitle(&format_device_subtitle(status, device));

    let suffix = gtk::Box::new(gtk::Orientation::Horizontal, 12);

    let scale = gtk::Scale::with_range(gtk::Orientation::Horizontal, 0.0, 100.0, 1.0);
    scale.set_draw_value(false);
    scale.set_size_request(140, -1);
    scale.set_sensitive(device.has_pipewire_sink);
    scale.set_value(current_volume(status, device) as f64);

    let route_button = gtk::Button::with_label(if is_routed(status, device) {
        "Unroute"
    } else {
        "Route"
    });
    route_button.set_sensitive(device.has_pipewire_sink);

    {
        let client = client.clone();
        let sender = sender.clone();
        let device_name = device.name.clone();
        let routed = is_routed(status, device);
        route_button.connect_clicked(move |_| {
            let client = client.clone();
            let sender = sender.clone();
            let device_name = device_name.clone();
            thread::spawn(move || {
                let result = if routed {
                    client.unroute(&device_name)
                } else {
                    client.route(&device_name, false)
                };
                let message = match result.and_then(|_| client.status()) {
                    Ok(status) => Message::Status(status),
                    Err(error) => Message::Error(error.to_string()),
                };
                let _ = sender.send(message);
            });
        });
    }

    {
        let client = client.clone();
        let sender = sender.clone();
        let device_name = device.name.clone();
        scale.connect_value_changed(move |scale| {
            let percent = scale.value().round() as i32;
            let client = client.clone();
            let sender = sender.clone();
            let device_name = device_name.clone();
            thread::spawn(move || {
                let message = match client.volume(&device_name, percent).and_then(|_| client.status()) {
                    Ok(status) => Message::Status(status),
                    Err(error) => Message::Error(error.to_string()),
                };
                let _ = sender.send(message);
            });
        });
    }

    suffix.append(&scale);
    suffix.append(&route_button);
    row.add_suffix(&suffix);
    row
}

fn section_title(text: &str) -> gtk::Label {
    let label = gtk::Label::new(Some(text));
    label.set_xalign(0.0);
    label.add_css_class("title-4");
    label.set_margin_top(12);
    label.set_margin_bottom(6);
    label
}

fn info_row(title: &str, subtitle: &str) -> adw::ActionRow {
    adw::ActionRow::builder()
        .title(title)
        .subtitle(subtitle)
        .activatable(false)
        .build()
}

fn format_device_subtitle(status: &Status, device: &Device) -> String {
    let mut parts = Vec::new();
    if !device.model.is_empty() {
        parts.push(device.model.clone());
    }
    if !device.address.is_empty() {
        parts.push(device.address.clone());
    }
    if !device.protocols.is_empty() {
        parts.push(device.protocols.join(","));
    }
    if device.has_pipewire_sink {
        parts.push(format!(
            "PipeWire: {} ({})",
            device.pipewire_sink_name, device.pipewire_sink_backend
        ));
    } else {
        parts.push("No PipeWire RAOP sink".to_string());
    }
    if is_routed(status, device) {
        parts.push("Active".to_string());
    }
    parts.join(" • ")
}

fn current_volume(status: &Status, device: &Device) -> i32 {
    status
        .routes
        .iter()
        .find(|route| route.device_id == device.id)
        .map(|route| route.volume)
        .unwrap_or_else(|| {
            if device.pipewire_sink_volume > 0 {
                device.pipewire_sink_volume
            } else {
                100
            }
        })
}

fn is_routed(status: &Status, device: &Device) -> bool {
    status.routes.iter().any(|route| route.device_id == device.id)
}

fn device_name_for(status: &Status, id: &str) -> String {
    status
        .devices
        .iter()
        .find(|device| device.id == id)
        .map(|device| device.name.clone())
        .unwrap_or_else(|| id.to_string())
}

fn clear_box(container: &gtk::Box) {
    while let Some(child) = container.first_child() {
        container.remove(&child);
    }
}
