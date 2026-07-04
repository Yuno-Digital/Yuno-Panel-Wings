#!/usr/bin/env bash
#
# Yuno Panel Wings (node daemon) installer for Debian/Ubuntu.
#
# Installs Docker and Go, builds the daemon, configures it against your panel
# and runs it as a systemd service.
#
#   curl -fsSL https://raw.githubusercontent.com/Yuno-Digital/Yuno-Panel-Wings/main/install.sh | bash -s -- \
#       --panel-url https://panel.example.com --token yuno_node_… --node 1
#
#   # or interactively (it will ask for the panel URL, token and node id):
#   curl -fsSL https://raw.githubusercontent.com/Yuno-Digital/Yuno-Panel-Wings/main/install.sh | bash
#
set -euo pipefail

# System binaries live in sbin, which isn't always on the PATH inherited by
# `curl | bash`. Include Go's install location too.
export PATH="/usr/local/go/bin:/usr/local/sbin:/usr/sbin:/sbin:$PATH"

REPO="https://github.com/Yuno-Digital/Yuno-Panel-Wings.git"
BRANCH="main"
SRC="/opt/yuno-wings"
BIN="/usr/local/bin/yuno-wings"
CONF="/etc/yuno"
DATA="/var/lib/yuno/servers"
SERVICE="/etc/systemd/system/yuno-wings.service"
GO_MIN="1.26"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!  \033[0m %s\n' "$*"; }
die()  { printf '\033[1;31mx  \033[0m %s\n' "$*" >&2; exit 1; }
ask()  { local p="$1" v=""; printf '%s' "$p" >/dev/tty; read -r v </dev/tty || v=""; printf '%s' "$v"; }

# --- Parse arguments (all optional; missing ones are prompted for) -----------
PANEL_URL="${PANEL_URL:-}"
NODE_TOKEN="${NODE_TOKEN:-}"
NODE_ID="${NODE_ID:-}"
while [ $# -gt 0 ]; do
    case "$1" in
        --panel-url) PANEL_URL="${2:-}"; shift 2 ;;
        --token)     NODE_TOKEN="${2:-}"; shift 2 ;;
        --node)      NODE_ID="${2:-}"; shift 2 ;;
        --panel-url=*) PANEL_URL="${1#*=}"; shift ;;
        --token=*)     NODE_TOKEN="${1#*=}"; shift ;;
        --node=*)      NODE_ID="${1#*=}"; shift ;;
        *) warn "Ignoring unknown argument: $1"; shift ;;
    esac
done

# --- Root / sudo -------------------------------------------------------------
if [ "$(id -u)" -eq 0 ]; then SUDO=""; else
    command -v sudo >/dev/null 2>&1 || die "Please run as root or install sudo."
    SUDO="sudo"
fi

[ -f /etc/debian_version ] || warn "This script targets Debian/Ubuntu; other distros may need manual steps."

# --- Base packages -----------------------------------------------------------
log "Installing base packages (curl, git, ca-certificates)"
$SUDO apt-get update -qq
$SUDO DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ca-certificates curl git >/dev/null

# --- Docker ------------------------------------------------------------------
if command -v docker >/dev/null 2>&1; then
    log "Docker already installed ($(docker --version 2>/dev/null | cut -d, -f1))"
else
    log "Installing Docker via get.docker.com"
    curl -fsSL https://get.docker.com | $SUDO sh
fi
$SUDO systemctl enable --now docker >/dev/null 2>&1 || true

# --- Go (>= $GO_MIN) ---------------------------------------------------------
go_ok() {
    command -v go >/dev/null 2>&1 || return 1
    local v; v="$(go version 2>/dev/null | awk '{print $3}' | sed 's/^go//')"
    [ -n "$v" ] || return 1
    [ "$(printf '%s\n%s\n' "$GO_MIN" "$v" | sort -V | head -1)" = "$GO_MIN" ]
}

if go_ok; then
    log "Go already installed ($(go version | awk '{print $3}'))"
else
    case "$(uname -m)" in
        x86_64|amd64) GOARCH="amd64" ;;
        aarch64|arm64) GOARCH="arm64" ;;
        *) die "Unsupported architecture: $(uname -m)" ;;
    esac
    GOVER="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1)"
    [ -n "$GOVER" ] || die "Could not determine the latest Go version."
    log "Installing $GOVER ($GOARCH)"
    curl -fsSL "https://go.dev/dl/${GOVER}.linux-${GOARCH}.tar.gz" -o /tmp/go.tar.gz
    $SUDO rm -rf /usr/local/go
    $SUDO tar -C /usr/local -xzf /tmp/go.tar.gz
    rm -f /tmp/go.tar.gz
    echo 'export PATH=/usr/local/go/bin:$PATH' | $SUDO tee /etc/profile.d/go.sh >/dev/null
    go_ok || die "Go installation failed."
fi

# --- Build the daemon --------------------------------------------------------
if [ -d "$SRC/.git" ]; then
    log "Updating source in $SRC"
    $SUDO git -C "$SRC" fetch --all --quiet
    $SUDO git -C "$SRC" reset --hard "origin/$BRANCH" --quiet
else
    log "Cloning $REPO into $SRC"
    $SUDO rm -rf "$SRC"
    $SUDO git clone --quiet --branch "$BRANCH" "$REPO" "$SRC"
fi

log "Building yuno-wings"
$SUDO env PATH="$PATH" GOCACHE=/tmp/yuno-gocache sh -c "cd '$SRC' && go build -o '$BIN' ." \
    || die "Build failed."
log "Installed binary at $BIN"

# --- Directories -------------------------------------------------------------
$SUDO mkdir -p "$CONF" "$DATA"

# --- Configure against the panel --------------------------------------------
if [ -f "$CONF/config.json" ]; then
    log "Existing config found at $CONF/config.json — keeping it (delete it to reconfigure)."
else
    if [ -z "$PANEL_URL$NODE_TOKEN$NODE_ID" ] && [ -e /dev/tty ]; then
        log "Configure this node (from the panel: Admin → Nodes → your node → Auto Deploy)"
        [ -n "$PANEL_URL" ]  || PANEL_URL="$(ask 'Panel URL (https://…): ')"
        [ -n "$NODE_ID" ]    || NODE_ID="$(ask 'Node ID: ')"
        [ -n "$NODE_TOKEN" ] || NODE_TOKEN="$(ask 'Node token (yuno_node_…): ')"
    fi

    if [ -n "$PANEL_URL" ] && [ -n "$NODE_TOKEN" ] && [ -n "$NODE_ID" ]; then
        log "Fetching node config from the panel"
        $SUDO sh -c "cd '$CONF' && '$BIN' configure --panel-url '$PANEL_URL' --token '$NODE_TOKEN' --node '$NODE_ID'" \
            || die "configure failed — check the panel URL, token and node id."
    else
        warn "No panel details given — generating a default config.json instead."
        $SUDO sh -c "cd '$CONF' && '$BIN' >/dev/null 2>&1 || true"
        warn "Edit $CONF/config.json (set panel_url) or re-run: $BIN configure --panel-url … --token … --node …"
    fi
fi

# --- systemd service ---------------------------------------------------------
log "Installing systemd service"
$SUDO tee "$SERVICE" >/dev/null <<UNIT
[Unit]
Description=Yuno Panel Wings (node daemon)
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
WorkingDirectory=$CONF
ExecStart=$BIN
Restart=always
RestartSec=5
LimitNOFILE=4096

[Install]
WantedBy=multi-user.target
UNIT

$SUDO systemctl daemon-reload
$SUDO systemctl enable --now yuno-wings >/dev/null 2>&1 || $SUDO systemctl restart yuno-wings

# --- Health check ------------------------------------------------------------
sleep 2
PORT="$(grep -o '"api_port"[[:space:]]*:[[:space:]]*[0-9]*' "$CONF/config.json" 2>/dev/null | grep -o '[0-9]*$' || true)"
PORT="${PORT:-8090}"
if curl -fsS "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    log "Daemon is up and healthy on port ${PORT}."
else
    warn "Daemon did not answer /health yet — check: journalctl -u yuno-wings -n 50 --no-pager"
fi

echo
log "Done. Useful commands:"
echo "    systemctl status yuno-wings        # service status"
echo "    journalctl -u yuno-wings -f         # live logs"
echo "    $BIN configure --panel-url … --token … --node …   # reconfigure"
echo
log "Make sure the panel can reach this host on port ${PORT} (open the firewall if needed)."
