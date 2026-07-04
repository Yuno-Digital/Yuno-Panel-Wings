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
DOMAIN="${DOMAIN:-}"
SSL_EMAIL="${SSL_EMAIL:-}"
SERVER_IP4=""
SERVER_IP6=""
while [ $# -gt 0 ]; do
    case "$1" in
        --panel-url) PANEL_URL="${2:-}"; shift 2 ;;
        --token)     NODE_TOKEN="${2:-}"; shift 2 ;;
        --node)      NODE_ID="${2:-}"; shift 2 ;;
        --domain)    DOMAIN="${2:-}"; shift 2 ;;
        --ssl-email) SSL_EMAIL="${2:-}"; shift 2 ;;
        --panel-url=*) PANEL_URL="${1#*=}"; shift ;;
        --token=*)     NODE_TOKEN="${1#*=}"; shift ;;
        --node=*)      NODE_ID="${1#*=}"; shift ;;
        --domain=*)    DOMAIN="${1#*=}"; shift ;;
        --ssl-email=*) SSL_EMAIL="${1#*=}"; shift ;;
        *) warn "Ignoring unknown argument: $1"; shift ;;
    esac
done

# Whether a domain resolves (A or AAAA) to one of this server's public IPs.
domain_points_here() {
    local domain="$1"
    if [ -n "$SERVER_IP4" ] && dig +short A "$domain" 2>/dev/null | grep -qxF "$SERVER_IP4"; then return 0; fi
    if [ -n "$SERVER_IP6" ] && dig +short AAAA "$domain" 2>/dev/null | grep -qxF "$SERVER_IP6"; then return 0; fi
    return 1
}

# Obtain a Let's Encrypt certificate (certbot standalone, since the daemon
# serves TLS itself) and point config.json at it. Sets FQDN for the summary.
FQDN=""
setup_tls() {
    # Skip entirely when non-interactive and no --domain was given.
    if [ -z "$DOMAIN" ] && [ ! -e /dev/tty ]; then
        return 0
    fi

    log "Installing certbot, jq and dig (for optional HTTPS)"
    $SUDO DEBIAN_FRONTEND=noninteractive apt-get install -y -qq certbot jq >/dev/null 2>&1 || true
    command -v dig >/dev/null 2>&1 \
        || $SUDO apt-get install -y -qq bind9-dnsutils >/dev/null 2>&1 \
        || $SUDO apt-get install -y -qq dnsutils >/dev/null 2>&1 || true

    SERVER_IP4="$(curl -4 -fsSL --max-time 6 https://api.ipify.org 2>/dev/null || true)"
    SERVER_IP6="$(curl -6 -fsSL --max-time 6 https://api6.ipify.org 2>/dev/null || true)"
    log "This server's public IP — IPv4: ${SERVER_IP4:-none} · IPv6: ${SERVER_IP6:-none}"

    # Resolve a domain that points here (loop only when interactive).
    while true; do
        if [ -z "$DOMAIN" ]; then
            [ -e /dev/tty ] || return 0
            DOMAIN="$(ask 'Domain for this node — HTTPS (empty to skip, use plain HTTP): ')"
            [ -z "$DOMAIN" ] && { warn "Skipping HTTPS — the node will serve plain HTTP."; return 0; }
        fi

        if domain_points_here "$DOMAIN"; then
            log "$DOMAIN points to this server — continuing."
            break
        fi

        warn "$DOMAIN does not resolve to this server."
        warn "  $DOMAIN → A: $(dig +short A "$DOMAIN" 2>/dev/null | tr '\n' ' ')AAAA: $(dig +short AAAA "$DOMAIN" 2>/dev/null | tr '\n' ' ')"
        if [ ! -e /dev/tty ]; then
            warn "Point the DNS record at this server and re-run with --domain. Skipping HTTPS."
            return 0
        fi
        case "$(ask 'Enter a different domain? [Y/n]: ')" in [Nn]*) return 0 ;; esac
        DOMAIN=""
    done

    command -v jq >/dev/null 2>&1 || { warn "jq is not available — cannot write SSL paths. Skipping HTTPS."; return 0; }

    [ -n "$SSL_EMAIL" ] || { [ -e /dev/tty ] && SSL_EMAIL="$(ask "Email for Let's Encrypt (empty = register without email): ")"; }

    log "Requesting a certificate for $DOMAIN (certbot standalone, port 80)"
    if [ -n "$SSL_EMAIL" ]; then
        $SUDO certbot certonly --standalone -d "$DOMAIN" --non-interactive --agree-tos -m "$SSL_EMAIL" --keep-until-expiring \
            || { warn "certbot failed — is port 80 open and DNS correct? Leaving the node on HTTP."; return 0; }
    else
        $SUDO certbot certonly --standalone -d "$DOMAIN" --non-interactive --agree-tos --register-unsafely-without-email --keep-until-expiring \
            || { warn "certbot failed — is port 80 open and DNS correct? Leaving the node on HTTP."; return 0; }
    fi

    local cert="/etc/letsencrypt/live/${DOMAIN}/fullchain.pem"
    local key="/etc/letsencrypt/live/${DOMAIN}/privkey.pem"

    # Point config.json at the certificate.
    local updated
    if updated="$($SUDO jq --arg c "$cert" --arg k "$key" '.ssl_cert=$c | .ssl_key=$k' "$CONF/config.json")"; then
        printf '%s\n' "$updated" | $SUDO tee "$CONF/config.json" >/dev/null
        $SUDO chmod 600 "$CONF/config.json"
    else
        warn "Could not write SSL paths to config.json — set ssl_cert/ssl_key manually."
        return 0
    fi

    # Restart the daemon automatically after each renewal.
    $SUDO mkdir -p /etc/letsencrypt/renewal-hooks/deploy
    printf '#!/bin/sh\nsystemctl restart yuno-wings\n' | $SUDO tee /etc/letsencrypt/renewal-hooks/deploy/yuno-wings.sh >/dev/null
    $SUDO chmod +x /etc/letsencrypt/renewal-hooks/deploy/yuno-wings.sh

    FQDN="$DOMAIN"
    log "HTTPS enabled — the daemon will serve TLS for $DOMAIN."
}

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

# --- Optional HTTPS (Let's Encrypt) -----------------------------------------
setup_tls

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
# HTTPS when a certificate is configured (curl -k: the cert is for the domain,
# not 127.0.0.1).
if grep -q '"ssl_cert"[[:space:]]*:[[:space:]]*"[^"]\+"' "$CONF/config.json" 2>/dev/null; then
    SCHEME="https"; CURL_OPTS="-k"
else
    SCHEME="http"; CURL_OPTS=""
fi
if curl -fsS $CURL_OPTS "${SCHEME}://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    log "Daemon is up and healthy (${SCHEME}) on port ${PORT}."
else
    warn "Daemon did not answer /health yet — check: journalctl -u yuno-wings -n 50 --no-pager"
fi

echo
log "Done. Useful commands:"
echo "    systemctl status yuno-wings        # service status"
echo "    journalctl -u yuno-wings -f         # live logs"
echo "    $BIN configure --panel-url … --token … --node …   # reconfigure"
echo
if [ -n "$FQDN" ]; then
    log "In the panel, set this node to FQDN '${FQDN}', port ${PORT}, and enable 'Use HTTPS (TLS)'."
fi
log "Make sure the panel can reach this host on port ${PORT} (open the firewall if needed)."
