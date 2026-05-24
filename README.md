# poweraudio

A terminal UI and daemon for managing audio output devices on Linux. Automatically switches to your Bluetooth headphones when they connect and falls back to your preferred device when they disconnect — not a random one.

![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/Platform-Linux-FCC624?logo=linux&logoColor=black)
![License](https://img.shields.io/badge/License-MIT-blue)

## The Problem

On most Linux desktops, connecting Bluetooth earphones doesn't automatically switch audio output to them. And when you disconnect, the system picks whatever device it feels like — often HDMI output nobody is using. You end up running `pactl` or digging through settings every time.

## The Solution

**poweraudio** runs a lightweight daemon that watches for Bluetooth connect/disconnect events via D-Bus and switches your audio output based on a priority list you configure through an interactive TUI.

```
poweraudio                          # open the TUI
poweraudio --daemon                 # run the daemon directly
```

## Features

**Daemon**
- Monitors Bluetooth device connections via D-Bus (BlueZ)
- Auto-switches audio output on connect based on your priority list
- Falls back to preferred device on disconnect (not random)
- Configurable behavior: always switch, only if higher priority, or never
- Works with both PipeWire and PulseAudio

**TUI**
- View all audio output devices with type, volume, and default status
- Switch default output device interactively
- Configure device priority by picking from detected devices
- Set connect/disconnect behavior with radio-button toggles
- Install/uninstall the daemon as a systemd service from the UI
- Auto-setup: if the daemon isn't running, offers to start or install it

## Installation

### From source

Requires Go 1.22+.

```bash
git clone https://github.com/roverflow/poweraudio.git
cd poweraudio
go mod tidy
make build
```

Install to `~/.local/bin` and set up the systemd service:

```bash
make install
systemctl --user daemon-reload
systemctl --user enable --now poweraudio
```

Or just run `poweraudio` — it will offer to install the service for you.

### Uninstall

```bash
make uninstall
```

## Quick Start

```bash
# 1. Build
make build

# 2. Run — the setup screen will guide you
./poweraudio
```

The setup screen appears automatically and offers to:
- **Start the daemon as a background process** (stops on logout)
- **Install as a systemd service** (auto-starts on login, recommended)

After the daemon starts, the TUI opens with your audio devices.

## Usage

### TUI Navigation

| Key | Action |
|-----|--------|
| `d` | Devices screen |
| `p` | Config screen |
| `s` | Status screen |
| `q` | Quit |
| `r` | Refresh |

### Devices Screen

```
  Audio Output Devices

  *  JBL Tune 520BT          Bluetooth   ████████░░  80%
     Built-in Audio Stereo    Speaker     ██████████ 100%
     HDMI Output              HDMI        ██████░░░░  60%

  [Enter] Set default  [j/k] Navigate  [r] Refresh
```

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up/down |
| `Enter` | Set selected device as default |

### Config Screen

Two sections, toggle with `Tab`:

**Device Priority** — pick from real devices, reorder to set fallback preference:

```
  Device Priority (highest first)

  1. JBL Tune 520BT              bluetooth
  2. Built-in Audio Stereo       speaker

  Available Devices

     HDMI Output                 HDMI
     USB Headset                 USB

  [Enter] Add  [x] Remove  [J/K] Reorder  [w] Save
```

**Switching Behavior** — configure what happens on connect/disconnect:

```
  On Bluetooth Connect

  > (*) Always switch to it
    ( ) Only if higher priority than current
    ( ) Never auto-switch

  On Disconnect (Fallback)

    (*) Switch to highest priority available
  > ( ) Switch to previous device

  [Enter] Toggle  [w] Save
```

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate |
| `J` / `K` | Reorder priority (Shift+j/k) |
| `Enter` | Add device / toggle option |
| `x` | Remove from priorities |
| `Tab` | Switch between Devices and Switching sections |
| `w` | Save changes |

### Status Screen

```
  Daemon Status

  Backend:  pipewire
  Socket:   /run/user/1000/poweraudio.sock
  Uptime:   2h 14m
  Service:  enabled

  Recent Events

  14:32:01  bluetooth connected: JBL Tune 520BT (AA:BB:CC:DD:EE:FF)
  14:32:02  switched to JBL Tune 520BT
  13:15:44  bluetooth disconnected: JBL Tune 520BT
  13:15:44  fallback to Built-in Audio Stereo
```

| Key | Action |
|-----|--------|
| `i` | Install as systemd service |
| `u` | Uninstall service |
| `r` | Refresh |

## Configuration

Config is stored at `~/.config/poweraudio/config.toml` (auto-created on first run). You can edit it directly or use the TUI.

```toml
[general]
backend = "auto"           # "auto", "pipewire", or "pulseaudio"

[switching]
on_connect = "always"      # "always", "priority", "never"
on_disconnect = "priority" # "priority", "previous"
switch_delay_ms = 500      # delay for PipeWire to settle new devices

[[priority]]
match = "JBL Tune 520BT"
type = "bluetooth"

[[priority]]
match = "Built-in Audio Analog Stereo"
```

### Switching Options

| Setting | Values | Description |
|---------|--------|-------------|
| `on_connect` | `always` | Always switch to newly connected device |
| | `priority` | Only switch if the new device has higher priority |
| | `never` | Never auto-switch on connect |
| `on_disconnect` | `priority` | Fall back to highest-priority available device |
| | `previous` | Fall back to whatever was active before |

## Architecture

```
poweraudio (single binary)
├── --daemon mode
│   ├── D-Bus monitor (BlueZ) ──→ Bluetooth connect/disconnect events
│   ├── Audio event monitor ────→ pactl subscribe (works with PipeWire too)
│   ├── Priority switcher ─────→ wpctl set-default / pactl set-default-sink
│   └── IPC server ────────────→ Unix socket (JSON protocol)
│
└── TUI mode (default)
    ├── BubbleTea UI ──────────→ Devices, Config, Status screens
    └── IPC client ────────────→ Connects to daemon via Unix socket
```

- **No CGo** — audio control via `wpctl` / `pactl` / `pw-dump` subprocesses
- **No root required** — runs as a user service
- **Minimal dependencies** — BubbleTea for TUI, godbus for D-Bus, BurntSushi/toml for config

## Requirements

- Linux with PipeWire (recommended) or PulseAudio
- BlueZ for Bluetooth monitoring
- Go 1.22+ to build
- `wpctl` and `pw-dump` (PipeWire) or `pactl` (PulseAudio)

## License

MIT
