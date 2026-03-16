#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="$PREFIX/bin"
SHARE_DIR="$PREFIX/share"
APPLICATIONS_DIR="$SHARE_DIR/applications"
ICON_NAME="dev.tuxplay.gui"
ICON_SOURCE="$SCRIPT_DIR/cmd/tuxplay-gui/src/icon.png"
ICON_DEST_DIR="$SHARE_DIR/icons/hicolor/512x512/apps"
DESKTOP_SOURCE="$SCRIPT_DIR/cmd/tuxplay-gui/dev.tuxplay.gui.desktop"
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/tuxplay"
USER_SYSTEMD_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
USER_SYSTEMD_UNIT="$USER_SYSTEMD_DIR/tuxplay.service"
SYSTEM_SYSTEMD_DIR="/etc/systemd/system"
SYSTEM_SYSTEMD_UNIT="$SYSTEM_SYSTEMD_DIR/tuxplay.service"
SYSTEMD_MODE="${SYSTEMD_MODE:-user}"

need_cmd() {
    command -v "$1" >/dev/null 2>&1
}

install_build_deps() {
    local packages=()

    if need_cmd dnf; then
        packages=(go rust cargo gcc pkgconf-pkg-config gtk4-devel libadwaita-devel)
        echo "Installing build dependencies with dnf..."
        sudo dnf install -y "${packages[@]}"
        return
    fi

    if need_cmd apt-get; then
        packages=(golang-go rustc cargo build-essential pkg-config libgtk-4-dev libadwaita-1-dev)
        echo "Installing build dependencies with apt-get..."
        sudo apt-get update
        sudo apt-get install -y "${packages[@]}"
        return
    fi

    echo "Missing build dependencies and no supported package manager found." >&2
    echo "Install Go, Rust/Cargo, pkg-config, GTK4, and Libadwaita development packages." >&2
    exit 1
}

ensure_build_deps() {
    local missing=0

    if ! need_cmd go || ! need_cmd cargo || ! need_cmd pkg-config; then
        missing=1
    fi

    if ! pkg-config --exists gtk4 libadwaita-1 2>/dev/null; then
        missing=1
    fi

    if [[ "$missing" -eq 1 ]]; then
        install_build_deps
    fi
}

build_binaries() {
    echo "Building tuxplay daemon..."
    go build -o "$SCRIPT_DIR/tuxplay" ./cmd/tuxplay

    echo "Building tuxplay GUI..."
    cargo build --release --manifest-path "$SCRIPT_DIR/cmd/tuxplay-gui/Cargo.toml"
}

install_binaries() {
    echo "Installing binaries to $BIN_DIR..."
    sudo install -d "$BIN_DIR"
    sudo install -m 0755 "$SCRIPT_DIR/tuxplay" "$BIN_DIR/tuxplay"
    sudo install -m 0755 "$SCRIPT_DIR/cmd/tuxplay-gui/target/release/tuxplay-gui" "$BIN_DIR/tuxplay-gui"
}

install_desktop_assets() {
    echo "Installing desktop entry and icon..."
    sudo install -d "$APPLICATIONS_DIR"
    sudo install -d "$ICON_DEST_DIR"
    sudo install -m 0644 "$DESKTOP_SOURCE" "$APPLICATIONS_DIR/$ICON_NAME.desktop"
    sudo install -m 0644 "$ICON_SOURCE" "$ICON_DEST_DIR/$ICON_NAME.png"

    if need_cmd update-desktop-database; then
        update-desktop-database "$APPLICATIONS_DIR" >/dev/null 2>&1 || true
    fi

    if need_cmd gtk-update-icon-cache; then
        gtk-update-icon-cache -q -t "$SHARE_DIR/icons/hicolor" >/dev/null 2>&1 || true
    fi
}

install_user_systemd_unit() {
    echo "Installing user systemd unit..."
    mkdir -p "$USER_SYSTEMD_DIR" "$STATE_DIR"
    cat >"$USER_SYSTEMD_UNIT" <<EOF
[Unit]
Description=TuxPlay daemon
After=pipewire.service wireplumber.service

[Service]
ExecStart=$BIN_DIR/tuxplay daemon
Restart=on-failure
RestartSec=2
Environment=XDG_STATE_HOME=${XDG_STATE_HOME:-$HOME/.local/state}

[Install]
WantedBy=default.target
EOF

    if need_cmd systemctl; then
        systemctl --user daemon-reload
        systemctl --user enable --now tuxplay.service
    fi
}

install_system_systemd_unit() {
    local run_user="${SUDO_USER:-$USER}"
    local run_uid
    local run_home
    run_uid="$(id -u "$run_user")"
    run_home="$(getent passwd "$run_user" | cut -d: -f6)"

    echo "Installing system systemd unit for user $run_user..."
    sudo install -d "$SYSTEM_SYSTEMD_DIR"
    sudo tee "$SYSTEM_SYSTEMD_UNIT" >/dev/null <<EOF
[Unit]
Description=TuxPlay daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$run_user
Environment=XDG_RUNTIME_DIR=/run/user/$run_uid
Environment=XDG_STATE_HOME=$run_home/.local/state
ExecStart=$BIN_DIR/tuxplay daemon
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF

    if need_cmd systemctl; then
        sudo systemctl daemon-reload
        sudo systemctl enable --now tuxplay.service
    fi
}

main() {
    cd "$SCRIPT_DIR"
    ensure_build_deps
    build_binaries
    install_binaries
    install_desktop_assets
    install_user_systemd_unit

    if [[ "$SYSTEMD_MODE" == "system" || "$SYSTEMD_MODE" == "both" ]]; then
        install_system_systemd_unit
    fi

    cat <<EOF

Install complete.

Installed binaries:
  $BIN_DIR/tuxplay
  $BIN_DIR/tuxplay-gui

Desktop entry:
  $APPLICATIONS_DIR/$ICON_NAME.desktop

Icon:
  $ICON_DEST_DIR/$ICON_NAME.png

User systemd service:
  $USER_SYSTEMD_UNIT

Optional system systemd service:
  $SYSTEM_SYSTEMD_UNIT
  install with: SYSTEMD_MODE=system ./install.sh
  or both with: SYSTEMD_MODE=both ./install.sh

Next checks:
  systemctl --user status tuxplay.service
  tuxplay status
  tuxplay-gui
EOF
}

main "$@"
