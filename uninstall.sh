#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${HOME}/.local/bin"
SERVICE_NAME="poweraudio"
SERVICE_DIR="${HOME}/.config/systemd/user"
SERVICE_FILE="${SERVICE_DIR}/${SERVICE_NAME}.service"
CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/poweraudio"
RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
SOCKET_FILE="${RUNTIME_DIR}/${SERVICE_NAME}.sock"
BINARY="${INSTALL_DIR}/${SERVICE_NAME}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

info()  { printf "${BLUE}::${RESET} %s\n" "$*" >&2; }
ok()    { printf "${GREEN}✓${RESET} %s\n" "$*" >&2; }
warn()  { printf "${YELLOW}!${RESET} %s\n" "$*" >&2; }
fail()  { printf "${RED}✗${RESET} %s\n" "$*" >&2; exit 1; }
skip()  { printf "  %s\n" "$*" >&2; }

PURGE=false
YES=false

usage() {
    cat >&2 <<EOF
Usage: $(basename "$0") [OPTIONS]

Remove poweraudio binary, systemd service, and runtime files.

Options:
  --purge       Also remove configuration (~/.config/poweraudio/)
  --yes, -y     Skip confirmation prompt
  --help, -h    Show this help
EOF
    exit 0
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --purge)  PURGE=true ;;
            --yes|-y) YES=true ;;
            --help|-h) usage ;;
            *) fail "Unknown option: $1 (see --help)" ;;
        esac
        shift
    done
}

detect() {
    local found=0

    if systemctl --user is-active "$SERVICE_NAME" &>/dev/null; then
        info "Service is running"
        found=1
    elif systemctl --user is-enabled "$SERVICE_NAME" &>/dev/null; then
        info "Service is enabled (not running)"
        found=1
    fi

    if [[ -x "$BINARY" ]]; then
        local ver
        ver=$("$BINARY" --version 2>/dev/null | awk '{print $2}' || echo "unknown")
        info "Binary: ${BINARY} (${ver})"
        found=1
    fi

    if [[ -f "$SERVICE_FILE" ]]; then
        info "Service unit: ${SERVICE_FILE}"
        found=1
    fi

    if [[ -S "$SOCKET_FILE" ]]; then
        info "Socket: ${SOCKET_FILE}"
        found=1
    fi

    if [[ -d "$CONFIG_DIR" ]]; then
        if $PURGE; then
            info "Config: ${CONFIG_DIR} (will be removed)"
        else
            info "Config: ${CONFIG_DIR} (kept — use --purge to remove)"
        fi
    fi

    local orphan_pids
    orphan_pids=$(pgrep -f "poweraudio --daemon" 2>/dev/null || true)
    if [[ -n "$orphan_pids" ]]; then
        info "Orphan daemon process(es): ${orphan_pids//$'\n'/, }"
        found=1
    fi

    if (( ! found )) && { ! $PURGE || [[ ! -d "$CONFIG_DIR" ]]; }; then
        warn "Nothing to uninstall"
        exit 0
    fi
}

confirm() {
    if $YES; then
        return 0
    fi

    echo "" >&2
    printf "${BOLD}Proceed with uninstall?${RESET} [y/N] " >&2
    read -r answer
    case "$answer" in
        [yY]|[yY][eE][sS]) return 0 ;;
        *) info "Aborted"; exit 0 ;;
    esac
}

stop_service() {
    if ! command -v systemctl &>/dev/null; then
        return
    fi

    if systemctl --user is-active "$SERVICE_NAME" &>/dev/null; then
        systemctl --user stop "$SERVICE_NAME" 2>/dev/null
        ok "Stopped service"
    fi

    if systemctl --user is-enabled "$SERVICE_NAME" &>/dev/null; then
        systemctl --user disable "$SERVICE_NAME" 2>/dev/null
        ok "Disabled service"
    fi
}

kill_orphans() {
    local pids
    pids=$(pgrep -f "poweraudio --daemon" 2>/dev/null || true)
    if [[ -z "$pids" ]]; then
        return
    fi

    for pid in $pids; do
        kill "$pid" 2>/dev/null && ok "Killed orphan daemon (PID ${pid})" || true
    done

    sleep 0.5
    for pid in $pids; do
        if kill -0 "$pid" 2>/dev/null; then
            kill -9 "$pid" 2>/dev/null && warn "Force-killed daemon (PID ${pid})" || true
        fi
    done
}

remove_files() {
    if [[ -f "$BINARY" ]]; then
        rm -f "$BINARY"
        ok "Removed ${BINARY}"
    else
        skip "Binary not found (already removed)"
    fi

    if [[ -f "$SERVICE_FILE" ]]; then
        rm -f "$SERVICE_FILE"
        ok "Removed ${SERVICE_FILE}"
    else
        skip "Service unit not found (already removed)"
    fi

    if command -v systemctl &>/dev/null; then
        systemctl --user daemon-reload 2>/dev/null
        ok "Reloaded systemd"
    fi

    if [[ -S "$SOCKET_FILE" ]]; then
        rm -f "$SOCKET_FILE"
        ok "Removed ${SOCKET_FILE}"
    elif [[ -e "$SOCKET_FILE" ]]; then
        rm -f "$SOCKET_FILE"
        ok "Removed stale ${SOCKET_FILE}"
    fi
}

remove_config() {
    if ! $PURGE; then
        return
    fi

    if [[ -d "$CONFIG_DIR" ]]; then
        rm -rf "$CONFIG_DIR"
        ok "Removed ${CONFIG_DIR}"
    else
        skip "Config directory not found (already removed)"
    fi
}

main() {
    parse_args "$@"

    printf "${BOLD}poweraudio uninstaller${RESET}\n\n"

    detect
    confirm

    echo "" >&2
    info "Uninstalling..."

    stop_service
    kill_orphans
    remove_files
    remove_config

    echo "" >&2
    printf "${GREEN}${BOLD}Uninstalled successfully${RESET}\n"

    if ! $PURGE && [[ -d "$CONFIG_DIR" ]]; then
        warn "Config preserved at ${CONFIG_DIR} (re-run with --purge to remove)"
    fi
}

main "$@"
