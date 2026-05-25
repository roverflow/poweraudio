#!/usr/bin/env bash
set -euo pipefail

REPO="https://github.com/roverflow/poweraudio.git"
INSTALL_DIR="${HOME}/.local/bin"
SERVICE_NAME="poweraudio"
SERVICE_DIR="${HOME}/.config/systemd/user"
SERVICE_FILE="${SERVICE_DIR}/${SERVICE_NAME}.service"
BINARY="${INSTALL_DIR}/${SERVICE_NAME}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

info()  { printf "${BLUE}::${RESET} %s\n" "$*"; }
ok()    { printf "${GREEN}✓${RESET} %s\n" "$*"; }
warn()  { printf "${YELLOW}!${RESET} %s\n" "$*"; }
fail()  { printf "${RED}✗${RESET} %s\n" "$*"; exit 1; }

check_deps() {
    local missing=()
    for cmd in go git install systemctl; do
        command -v "$cmd" &>/dev/null || missing+=("$cmd")
    done
    if (( ${#missing[@]} )); then
        fail "Missing dependencies: ${missing[*]}"
    fi
}

get_current_version() {
    if [[ -x "$BINARY" ]]; then
        "$BINARY" --version 2>/dev/null | awk '{print $2}'
    else
        echo "none"
    fi
}

build_in_tmpdir() {
    local tmpdir
    tmpdir=$(mktemp -d)
    trap "rm -rf '$tmpdir'" EXIT

    info "Cloning repository..."
    git clone --depth 1 "$REPO" "$tmpdir/poweraudio" 2>&1 | tail -1
    ok "Cloned"

    info "Building..."
    cd "$tmpdir/poweraudio"
    local ver
    ver=$(git describe --tags --always 2>/dev/null || echo "dev")
    go build -ldflags "-X main.version=${ver}" -o poweraudio ./cmd/poweraudio
    ok "Built version ${ver}"

    printf "%s" "$tmpdir/poweraudio"
}

build_in_place() {
    info "Pulling latest changes..."
    git pull --ff-only 2>&1 | tail -1
    ok "Updated source"

    info "Building..."
    local ver
    ver=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    go build -ldflags "-X main.version=${ver}" -o poweraudio ./cmd/poweraudio
    ok "Built version ${ver}"

    printf "%s" "$(pwd)"
}

install_binary() {
    local build_dir="$1"
    mkdir -p "$INSTALL_DIR"
    install -Dm755 "${build_dir}/poweraudio" "$BINARY"
    ok "Installed binary to ${BINARY}"
}

install_service() {
    local build_dir="$1"
    local unit_src="${build_dir}/configs/poweraudio.service"

    mkdir -p "$SERVICE_DIR"

    if [[ -f "$unit_src" ]]; then
        install -Dm644 "$unit_src" "$SERVICE_FILE"
    else
        cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=poweraudio - Audio output controller daemon
After=pipewire.service wireplumber.service
Wants=pipewire.service

[Service]
Type=simple
ExecStart=${BINARY} --daemon
Restart=on-failure
RestartSec=5
Environment=XDG_RUNTIME_DIR=%t

[Install]
WantedBy=default.target
EOF
    fi
    ok "Installed service unit"
}

restart_daemon() {
    info "Reloading systemd and restarting service..."
    systemctl --user daemon-reload

    if systemctl --user is-enabled "$SERVICE_NAME" &>/dev/null; then
        systemctl --user restart "$SERVICE_NAME"
    else
        systemctl --user enable --now "$SERVICE_NAME"
    fi
    ok "Service restarted"
}

verify() {
    info "Verifying installation..."
    local errors=0

    if [[ ! -x "$BINARY" ]]; then
        fail "Binary not found at ${BINARY}"
    fi
    local new_ver
    new_ver=$("$BINARY" --version 2>/dev/null | awk '{print $2}')
    ok "Binary version: ${new_ver}"

    sleep 1
    if systemctl --user is-active "$SERVICE_NAME" &>/dev/null; then
        ok "Service is running"
    else
        warn "Service is not active — checking logs:"
        systemctl --user status "$SERVICE_NAME" --no-pager -l 2>&1 | head -15 || true
        errors=1
    fi

    local running_pid running_ver
    running_pid=$(systemctl --user show -p MainPID --value "$SERVICE_NAME" 2>/dev/null)
    if [[ "$running_pid" -gt 0 ]] 2>/dev/null; then
        running_ver=$(cat "/proc/${running_pid}/cmdline" 2>/dev/null | tr '\0' ' ' | grep -oP 'poweraudio' || true)
        if [[ -n "$running_ver" ]]; then
            ok "Daemon PID: ${running_pid}"
        fi
    fi

    if (( errors )); then
        warn "Installation completed with warnings"
        return 1
    fi
    return 0
}

main() {
    printf "${BOLD}poweraudio installer${RESET}\n\n"

    check_deps

    local old_ver
    old_ver=$(get_current_version)

    if [[ "$old_ver" != "none" ]]; then
        info "Current version: ${old_ver}"
    else
        info "Fresh install"
    fi

    local build_dir
    if [[ -f "go.mod" ]] && grep -q "github.com/roverflow/poweraudio" go.mod 2>/dev/null; then
        info "Running from source tree"
        build_dir=$(build_in_place)
    else
        build_dir=$(build_in_tmpdir)
    fi

    install_binary "$build_dir"
    install_service "$build_dir"
    restart_daemon

    local new_ver
    new_ver=$(get_current_version)

    echo ""
    if verify; then
        echo ""
        if [[ "$old_ver" == "none" ]]; then
            printf "${GREEN}${BOLD}Installed successfully${RESET} (${new_ver})\n"
        elif [[ "$old_ver" != "$new_ver" ]]; then
            printf "${GREEN}${BOLD}Updated successfully${RESET} (${old_ver} → ${new_ver})\n"
        else
            printf "${GREEN}${BOLD}Reinstalled successfully${RESET} (${new_ver})\n"
        fi
    else
        echo ""
        warn "Installed ${new_ver} but service may need attention"
    fi
}

main "$@"
