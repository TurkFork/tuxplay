# TuxPlay

TuxPlay is a Linux AirPlay control plane built on top of PipeWire.

It does not try to replace PipeWire's RAOP support. Instead, TuxPlay discovers AirPlay devices, maps them to existing `raop_sink.*` PipeWire sinks, creates a `TuxPlay` output sink for desktop apps, and manages routing, volume, device state, and group state through a local daemon.

## Current Architecture

```text
Rust GTK GUI / CLI
        |
 Unix socket API
        |
 tuxplay daemon (Go)
        |
     PipeWire
        |
 AirPlay / RAOP sinks
```

The daemon owns:

- AirPlay discovery over `_airplay._tcp` and `_raop._tcp`
- PipeWire sink creation and RAOP sink mapping
- route and unroute operations through loopback modules
- persistent device, route, and group state

The clients are thin:

- `tuxplay` CLI talks to the daemon over `/tmp/tuxplay.sock`
- `tuxplay-gui` is a GTK4 + Libadwaita desktop client that uses the same socket API

## What Works Today

- daemon mode via `tuxplay daemon`
- continuous mDNS discovery
- normalized device listing with PipeWire sink mapping
- automatic creation of a PipeWire null sink named `tuxplay_output`
- detection of existing PipeWire RAOP sinks such as `raop_sink.Bedroom.local...`
- `route`, `unroute`, `volume`, `mute`, `pause`, `resume`, `stop`
- group creation, add, remove, and play state
- JSON-backed state persistence
- Rust GTK GUI talking to the daemon socket

## Not Implemented Yet

- native PipeWire client backend instead of `pactl` orchestration
- MPRIS or DBus desktop integration
- Homebridge or HomeKit integration
- advanced playback transport controls beyond what the current PipeWire-backed flow exposes
- true combine-sink based multi-room backend

## Commands

```bash
tuxplay daemon
tuxplay list
tuxplay status
tuxplay route "Bedroom"
tuxplay route --add "Living Room TV"
tuxplay unroute "Bedroom"
tuxplay volume "Bedroom" 50
tuxplay mute "Bedroom"
tuxplay pause "Bedroom"
tuxplay resume "Bedroom"
tuxplay stop "Bedroom"
tuxplay group create everywhere "Bedroom" "Office"
tuxplay group add everywhere "Kitchen"
tuxplay group remove everywhere "Kitchen"
tuxplay group play everywhere
```

## Building

### Go daemon and CLI

Requirements:

- Go
- PipeWire
- `pactl`

Build:

```bash
go build -o tuxplay ./cmd/tuxplay
```

Run:

```bash
./tuxplay daemon
```

### Rust GTK GUI

Requirements:

- Rust toolchain (`cargo`, `rustc`)
- GTK4 development headers
- Libadwaita development headers

On Fedora:

```bash
sudo dnf install gtk4-devel libadwaita-devel
```

Build:

```bash
cargo build --release --manifest-path ./cmd/tuxplay-gui/Cargo.toml
```

Run:

```bash
./cmd/tuxplay-gui/target/release/tuxplay-gui
```

## Installing

Install the CLI and daemon:

```bash
sudo install -Dm755 ./tuxplay /usr/local/bin/tuxplay
```

Install the GUI:

```bash
sudo install -Dm755 ./cmd/tuxplay-gui/target/release/tuxplay-gui /usr/local/bin/tuxplay-gui
```

Optional user service:

Create `~/.config/systemd/user/tuxplay.service`:

```ini
[Unit]
Description=TuxPlay daemon
After=pipewire.service wireplumber.service

[Service]
ExecStart=/usr/local/bin/tuxplay daemon
Restart=on-failure

[Install]
WantedBy=default.target
```

Enable it:

```bash
systemctl --user daemon-reload
systemctl --user enable --now tuxplay.service
```

## Debugging

Useful commands while testing:

```bash
wpctl status
pactl list short sinks
pactl list short modules
avahi-browse -rt _airplay._tcp
avahi-browse -rt _raop._tcp
tuxplay status
```

## Repository Layout

```text
cmd/
  tuxplay/       Go CLI and daemon entrypoint
  tuxplay-gui/   Rust GTK4 + Libadwaita desktop client
internal/
  api/           Go daemon client helpers
  controller/    route, volume, mute, pause, resume logic
  daemon/        Unix socket HTTP server
  discovery/     mDNS device discovery and normalization
  group/         group management
  model/         shared daemon data models
  pipewire/      PipeWire and pactl orchestration
  state/         persisted device, route, and group state
```

## Notes

- The GUI is a thin client and requires the daemon to be running.
- The daemon socket defaults to `/tmp/tuxplay.sock`.
- State defaults to `~/.local/state/tuxplay/state.json`.
- The current PipeWire backend is pragmatic and shell-based. It is intended to be replaced with a native client implementation later.
