# poweraudio

A daemon and terminal UI that moves your Linux audio output to your Bluetooth
headphones when they connect, and puts it back somewhere sensible when they
disconnect.

Linux desktops mostly get the first half of that wrong and the second half
badly. Connect your earbuds and audio keeps playing through the speakers.
Disconnect them and the session picks whatever sink it likes, which on a lot of
machines is the HDMI output feeding a monitor with no speakers. poweraudio
watches BlueZ over D-Bus and switches the default sink itself, using a ranked
list of devices you set once.

```
poweraudio            # terminal UI
poweraudio --daemon   # daemon in the foreground
```

## Flags

| Flag | Meaning |
|------|---------|
| `--daemon` | run the daemon in the foreground instead of the UI |
| `--config PATH` | read and write this file instead of the default |
| `--version` | print the version and exit |

The daemon writes config changes back to whichever file it was started with, so
`--config` on the unit and `--config` on the UI do not have to agree for the UI
to work: the UI talks to the daemon, and the daemon owns the file.

## Requirements

- Linux with PipeWire or PulseAudio
- `wpctl` and `pw-dump` for the PipeWire backend, `pactl` for either
- BlueZ on the system bus, for the Bluetooth half
- systemd user session, if you want the daemon to start on login
- `notify-send` for desktop notifications, optional
- Go 1.25 or newer to build (`go.mod` pins 1.25.10)

Everything runs as your user. Nothing needs root.

## Install

From a checkout:

```bash
git clone https://github.com/roverflow/poweraudio.git
cd poweraudio
make install
```

That builds the binary into `~/.local/bin/poweraudio` and drops a unit file at
`~/.config/systemd/user/poweraudio.service`. Then:

```bash
systemctl --user daemon-reload
systemctl --user enable --now poweraudio
```

`install.sh` does the same thing plus a version check and a post-install
health check. `make build` alone just produces `./poweraudio` in the checkout.

You can skip all of it and run `poweraudio`. If nothing is listening on the
socket, the first screen offers to start a daemon for this session or install
the user service, and gets out of the way once one is up.

## Removing it

```bash
./uninstall.sh          # binary, unit file, socket, stray daemons
./uninstall.sh --purge  # the above plus ~/.config/poweraudio
```

`make uninstall` and `make purge` do the same from a checkout.

## How switching works

Worth reading once, because it explains every delay you will notice.

BlueZ publishes a `PropertiesChanged` signal on the system bus whenever a
device's `Connected` property flips. The daemon subscribes under the
`/org/bluez` path namespace, pulls the MAC out of the object path
(`/org/bluez/hci0/dev_3C_B0_ED_3A_2C_42`) and reads the `Alias` property for a
human name.

Bluetooth connecting is not the same event as an audio sink appearing.
PipeWire creates the `bluez_output.*` sink some time after BlueZ reports the
link, so the daemon waits `switch_delay_ms` (500 by default), re-lists sinks,
and looks for one whose MAC matches or whose name contains the BlueZ alias. If
the sink still is not there, the event is parked and retried every 500ms until
15 seconds have passed. The timer is only a safety net: `pactl subscribe`
reports the sink appearing, and that event triggers the switch directly, so a
slow adapter usually lands the moment its sink shows up rather than on the next
tick.

Once it finds the sink it runs `wpctl set-default <id>`, or
`pactl set-default-sink <name>` on the PulseAudio backend.

Disconnect is the same shape in reverse. The daemon waits 300ms for the sink to
vanish and re-lists. If the sink that was playing is still there, the device
that disconnected was not the one you were listening to and nothing moves.
Otherwise it picks a fallback: either the highest ranked entry in your priority
list that is present, or the sink that was default before it switched away,
depending on `on_disconnect`.

A priority entry matches a device when `match` is a case-insensitive substring
of the device's name, description or MAC address. If the entry also sets
`type`, the device's detected type has to equal it. Order in the file is the
ranking, first is highest.

The PipeWire backend decides a device's type from its `pw-dump` properties:
`device.api` of `bluez5` or a `node.name` starting with `bluez_` means
Bluetooth, `device.bus` of `usb` means USB, a name containing `hdmi` or
`displayport` means HDMI, a name containing `headphone` means Headphone, and
anything left is Speaker.

## Keys

Anywhere:

| Key | Action |
|-----|--------|
| `d` `p` `s` | devices, config, status (or `1` `2` `3`) |
| `?` | toggle the key reference, `esc` closes it |
| `r` | refresh from the daemon |
| `q` | quit, asks once if the config screen has unsaved edits |
| `ctrl+c` | quit without asking |

Devices:

| Key | Action |
|-----|--------|
| `j` `k` | move, also `g` `G` `pgup` `pgdn` |
| `enter` | make the selected device the default output |
| `h` `l` | volume down and up in 5% steps, 0 to 150 |
| `m` | mute or unmute |

Config, priorities section:

| Key | Action |
|-----|--------|
| `tab` | swap to the switching rules |
| `j` `k` | move, falling off the bottom of the ranked list enters the device list below it |
| `J` `K` | move the selected entry up or down the ranking |
| `enter` | add the highlighted device to the ranking |
| `x` | drop the selected entry |
| `w` | write to the config file |

Config, switching section:

| Key | Action |
|-----|--------|
| `tab` | swap back to priorities |
| `j` `k` | move |
| `enter` or `space` | pick the option under the cursor |
| `w` | write to the config file |

Status:

| Key | Action |
|-----|--------|
| `j` `k` | scroll the event log, `g` jumps to newest |
| `i` | write and enable the systemd user unit |
| `u` | disable and delete it |

Edits on the config screen are not saved until you press `w`. A yellow
`unsaved` marker sits next to the heading until you do, and `q` asks once
before throwing the work away.

## Configuration

`~/.config/poweraudio/config.toml`, created on first run. Honours
`XDG_CONFIG_HOME`.

```toml
[general]
backend = "auto"            # auto, pipewire, pulseaudio

[switching]
on_connect = "always"       # always, priority, never
on_disconnect = "priority"  # priority, previous
switch_delay_ms = 500

[notifications]
enabled = true
on_device_change = true

[daemon]
socket_path = "/run/user/1000/poweraudio.sock"

[[priority]]
match = "JBL Tune 520BT"
type = "bluetooth"

[[priority]]
match = "Built-in Audio Analog Stereo"
```

`backend = "auto"` tries `wpctl status` first and falls back to `pactl info`.

`on_connect` decides what a Bluetooth device connecting is allowed to do.
`always` takes the output every time. `priority` only takes it when the new
device outranks whatever is playing, so plugging in earbuds while your USB
headset is on the list above them changes nothing. `never` leaves the switching
to you and keeps the daemon around for the event log and the UI.

`on_disconnect` picks the fallback. `priority` walks your ranking top down and
takes the first device that is present. `previous` returns to whatever was
default before the daemon switched away, and falls through to the ranking when
that device has gone too.

`switch_delay_ms` is the head start you give PipeWire to register the new sink
before the daemon goes looking for it. Raise it if your adapter is slow, though
the retries cover most of that already.

`socket_path` defaults to `$XDG_RUNTIME_DIR/poweraudio.sock`. The socket is
created with mode 0600.

Two fields that exist in the file and do nothing yet: `general.log_level` and
`tui.show_volume`. The daemon logs at a fixed level to stderr, which systemd
captures, and the UI always draws volume bars.

Pressing `w` in the UI rewrites the whole file from the daemon's in-memory
config, so comments you added by hand do not survive. Edit the file directly if
you want to keep them, and restart the daemon to pick the changes up.

## Reading the logs

The daemon keeps its last 200 events in memory and the status screen shows the
most recent 50, newest first, coloured by what happened. Failures are red,
switches and fallbacks are green, and skipped or abandoned switches are amber.
It is the fastest way to see why a switch did not happen.
`skipping switch: X has lower priority` means `on_connect` is set
to `priority` and your ranking said no. `giving up waiting for the audio sink
of X` means BlueZ connected but PipeWire never produced a sink, which is
usually a codec or profile problem rather than anything poweraudio can fix.

The same lines go to stderr, so `journalctl --user -u poweraudio -f` works when
the UI is not running.

## How it is put together

One binary, two modes.

`--daemon` runs the event loop. It holds a D-Bus subscription for BlueZ
property changes, a `pactl subscribe` pipe for sink and default-device changes,
and a Unix socket serving newline-delimited JSON requests. It shells out to
`wpctl`, `pw-dump` and `pactl` rather than linking against anything, so there
is no cgo and no libpipewire version to match.

Without `--daemon` you get the UI, built on Bubble Tea. It owns no audio state.
Every screen is a view over one JSON round trip to the daemon, refreshed on a
two second tick, and every action is a request back. Which means the UI can
come and go, and a daemon with no UI attached behaves identically.

```
internal/audio       backend interface, PipeWire and PulseAudio implementations
internal/bluetooth   BlueZ D-Bus subscription
internal/daemon      event loop, switching rules, IPC server
internal/ipc         wire protocol and client
internal/tui         Bubble Tea screens
internal/config      TOML load and save
```

## Troubleshooting

**The UI says the daemon is offline.** Check `systemctl --user status
poweraudio`. If the unit is not installed, press `i` on the status screen.

**Nothing switches when I connect.** Look at the status screen. No
`bluetooth connected` line means the D-Bus subscription never came up, so check
that `bluetoothd` is running. A `connected` line with nothing after it means the
sink never appeared, which you can confirm with `wpctl status` while the device
is connected.

**It switches to the wrong thing on disconnect.** Your priority list is either
empty or nothing on it is present, in which case the daemon falls back to the
first sink it can find. Add the devices you actually use on the config screen.

**A daemon that will not start.** `another poweraudio daemon is already
listening` means one is up already, usually the systemd unit. Find it with
`pgrep -af "poweraudio --daemon"` and stop that one first. The daemon refuses
to take over a live socket rather than leaving two of them fighting over the
default sink.

## Working on it

```bash
go build ./...
go vet ./...
go test -race ./...
```

The tests cover the parsing that talks to `wpctl`, `pactl` and BlueZ, the
priority matching, the config round trip, the IPC server, and the locking
around the daemon's shared state. They need no audio server: the backend is
stubbed and the samples are captured output.

## License

MIT
