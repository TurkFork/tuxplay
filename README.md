# TuxPlay

> Linux audio routing daemon and AirPlay control layer for PipeWire

TuxPlay transforms a Linux system into a network audio hub — routing audio from local applications to AirPlay devices with proper latency reporting, multi-room synchronization, and home automation support. Designed for Linux-based media hardware including the **Link Stereo** and **Link Box**.

---

## Why TuxPlay

PipeWire supports RAOP (AirPlay audio streaming) at the transport level, but stops there. It provides no centralized device management, playback control, multi-room routing, latency-aware synchronization, or home automation integration.

TuxPlay fills that gap. It sits above PipeWire as a control plane — handling everything from device discovery to lip-sync compensation — while PipeWire remains the audio fabric underneath.

---

## Architecture

```
Applications
     │
     ▼
 PipeWire
     │
     ▼
TuxPlay Output        ← virtual sink exposed to the system
     │
     ▼
 TuxPlay Daemon
     │
     ├── mDNS discovery
     ├── routing engine
     ├── latency manager
     ├── AirPlay transport (RTSP/RAOP)
     └── device & group control
     │
     ▼
AirPlay Devices
(HomePod, Apple TV, Roku, smart TVs, etc.)
```

---

## Features

### Core
- AirPlay device discovery via mDNS / Bonjour
- PipeWire virtual sink (`TuxPlay Output`)
- Single and multi-device audio routing
- Device capability detection

### Synchronization
- Per-device latency measurement
- PipeWire latency reporting for A/V sync
- Multi-room temporal alignment

### Control
- Per-device volume and mute
- Playback control
- Routing management

### Integration
- Homebridge support
- Home automation hooks
- CLI control interface
- Daemon API via Unix socket

### Platform targets
- Linux desktops and laptops
- Media centers and set-top boxes
- Embedded systems and stereo receivers

---

## Use Cases

**Send laptop audio to a HomePod**
```
Spotify → PipeWire → TuxPlay Output → TuxPlay → HomePod
```

**Multi-room playback**
```
TuxPlay Output
    ├── Living Room TV
    ├── Bedroom HomePod
    └── Kitchen Speaker
```

**Linux set-top box as audio hub**
```
Apps / HDMI input → PipeWire → TuxPlay → Network speakers
```

---

## Latency Handling

AirPlay devices introduce significant audio buffering — often around two seconds. TuxPlay measures this per device and reports it back to PipeWire, allowing video players to delay frame output accordingly. The result is proper lip-sync even when audio is rendered on a remote speaker.

---

## Current Status

TuxPlay is in early development.

**Done:**
- Daemon architecture and process lifecycle
- IPC Unix socket
- Structured logging
- AirPlay RTSP prototype
- mDNS discovery groundwork

**In progress:**
- PipeWire backend integration
- Routing engine
- Latency reporting pipeline
- Device state management

---

## CLI

```sh
# Daemon
tuxplay daemon

# Discovery & status
tuxplay list
tuxplay status

# Routing
tuxplay route "Bedroom HomePod"
tuxplay route --add "Living Room TV"
tuxplay unroute "Bedroom HomePod"

# Playback control
tuxplay volume "Bedroom HomePod" 50
tuxplay mute "Kitchen Speaker"

# Groups (planned)
tuxplay group create everywhere "Bedroom" "Office" "Kitchen"
tuxplay group play everywhere
tuxplay group add everywhere "Patio"
```

---

## Building

**Requirements:** Go 1.22+, PipeWire, Avahi (mDNS), Linux

```sh
go build ./cmd/tuxplay
./tuxplay daemon
```

---

## Debugging

```sh
# List PipeWire audio nodes
pw-cli list-objects Node

# Show full audio graph
wpctl status

# Scan for AirPlay devices on the network
avahi-browse -rt _airplay._tcp
```

---

## Project Structure

```
cmd/
  tuxplay/
internal/
  api/           # daemon API layer
  control/       # playback & volume
  discovery/     # mDNS / Bonjour
  groups/        # multi-room groups
  pipewire/      # PipeWire backend
  rtsp/          # AirPlay transport
  state/         # device state
README.md
go.mod
```

---

## Vision

TuxPlay aims to be the definitive open audio routing layer for Linux — a first-class alternative to proprietary multi-room ecosystems. The long-term roadmap includes full AirPlay 2 control, HomeKit and Matter integration, distributed audio routing across local networks, and deep smart home automation support.

---

## License

MIT