# poweraudio

A daemon, a terminal UI and a small command line tool that move your Linux
audio output to your Bluetooth headphones when they connect, and put it back
somewhere sensible when they disconnect.

Linux desktops mostly get the first half of that wrong and the second half
badly. Connect your earbuds and audio keeps playing through the speakers.
Disconnect them and the session picks whatever sink it likes, which on a lot of
machines is the HDMI output feeding a monitor with no speakers. poweraudio
watches BlueZ over D-Bus and switches the default sink itself, using a ranked
list of devices you set once.

```
poweraudio                 # terminal UI
poweraudio daemon          # daemon in the foreground
poweraudio next            # cycle the output, bind it to a media key
poweraudio watch           # one line per change, for a status bar
```

## Command line

```
poweraudio [--config PATH] [--version] [--daemon] [<command> [args]]
```

Without a command poweraudio opens the terminal UI.

| Command | What it does |
|---------|--------------|
| `list [--json]` | output devices, one per line, `*` on the default |
| `status [--json]` | default device, backend, config path, uptime, recent events |
| `set <query>` | make a device the default output |
| `next` | switch to the next available device, wrapping around |
| `volume <+N\|-N\|N> [--device Q]` | set or adjust the volume, 0 to 150 percent |
| `mute [--device Q]` | toggle mute and print the new state |
| `watch [--json]` | print a line every time the default device or its level changes |
| `reload` | make the daemon re-read its config file |
| `daemon` | run the daemon in the foreground, same as `--daemon` |
| `help` | print the usage text |

A query matches a device id, name, description or MAC address, exactly or as a
case-insensitive substring. `set razer` is enough when only one device is a
Razer; `set analog` on a machine with three analog outputs lists all three and
exits 1. `volume` and `mute` act on the default device unless `--device` says
otherwise.

Output is plain text with no colour. Exit codes: 0 on success, 1 when the daemon
is not running or refused the request, 2 for a bad command line.

```
$ poweraudio list
   Razer Barracuda X Analog Stereo           USB      45%  alsa_output.usb-1532_Razer_Barracuda_X-01.analog-stereo
*  Ryzen HD Audio Controller Analog Stereo   Speaker  53%  alsa_output.pci-0000_0e_00.6.analog-stereo

$ poweraudio watch
Ryzen HD Audio Controller Analog Stereo  53%
JBL Tune 520BT  80%
JBL Tune 520BT  muted
```

`--daemon` and `--config` work as before. The daemon writes config changes
back to whichever file it was started with, so `--config` on the unit and
`--config` on the UI do not have to agree: the UI talks to the daemon, and the
daemon owns the file.

## Requirements

- Linux with PipeWire (with pipewire-pulse, which every desktop install ships)
  or PulseAudio
- `pactl`, which comes with either
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
the user service, and gets out of the way once one is up. A daemon started for
the session logs to `~/.local/state/poweraudio/daemon.log`.

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
tick. A parked device is only forgotten when that same device disconnects, so
a second headset going away does not cancel the first one's switch.

Once it finds the sink it runs `pactl set-default-sink <name>`.

Disconnect is the same shape in reverse. The daemon waits 300ms for the sink to
vanish and re-lists. If the sink that was playing is still there and can still
play, the device that disconnected was not the one you were listening on and
nothing moves. Otherwise it picks a fallback: either the highest ranked entry
in your priority list that can play, or the sink that was default before it
switched away, depending on `on_disconnect`.

A sink can stay listed and still be unable to play. The active port is where
the audio is going, and pactl marks that port `not available` when nothing is
plugged into it. The daemon then runs the same fallback it runs when the sink
disappears. A port marked `availability unknown` stays usable. `SUSPENDED` is
only an idle sink, and those stay usable too.

The Barracuda X receiver is the exception that reports unknown either way.
Powering the earcups off does not remove the sound card. The dongle pushes one
HID report when that changes, report id `02`, and byte 13 is `01` while the
earcups are on and `00` when they are off. The daemon marks that sink unable
to play and runs the fallback. It does not repeat the report while the state
holds, so a daemon that starts with the earcups already off waits for the next
press. Reading the report needs the udev rule in `configs/70-poweraudio.rules`,
because the node is root-only without it.

A priority entry matches a device when `match` is a case-insensitive substring
of the device's name, its technical sink name or its MAC address. If the entry
also sets `type`, the device's detected type has to equal it. Order in the file
is the ranking, first is highest. The UI and the daemon share the one matcher.
The green dot also requires that the device can play, so a port that is not
available leaves the dot off.

Device types come from the properties pactl reports. `device.api` of `bluez5`,
an `api.bluez5.address`, or a sink name starting with `bluez_` means Bluetooth.
`device.form.factor` of `headphone` or `headset` means Headphone, `device.bus`
of `usb` means USB, a name containing `hdmi` or `displayport` means HDMI, and
anything left is Speaker. Plain PulseAudio publishes fewer of those properties,
so the names get a second look before a device is called a speaker.

## Keys

Anywhere:

| Key | Action |
|-----|--------|
| `d` `c` `s` | devices, config, status (or `1` `2` `3`) |
| `?` | toggle the key reference, `esc` closes it |
| `r` | refresh, and reconnect if the daemon went away |
| `q` | quit, asks once if the config screen has unsaved edits |
| `ctrl+c` | quit without asking |
| mouse | click a tab or a row, wheel scrolls the list under the pointer |

Devices:

| Key | Action |
|-----|--------|
| `j` `k` | move, also `g` `G` `pgup` `pgdn` |
| `enter` | make the selected device the default output, also a second click on it |
| `h` `l` | volume down and up in 5% steps, 0 to 150 |
| `H` `L` | volume in 1% steps |
| `m` | mute or unmute |

The panel under the list shows the selected device's technical sink name, MAC,
volume, and where it sits on the priority list. It disappears first when the
terminal is short.

Config, priorities section:

| Key | Action |
|-----|--------|
| `tab` | swap to the switching rules |
| `j` `k` | move, falling off the bottom of the ranked list enters the device list below it |
| `J` `K` | move the selected entry up or down the ranking |
| `enter` | add the highlighted device to the ranking, or play through the selected entry |
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
| `i` | write and enable the systemd user unit, shown only when it is not installed |
| `u` | stop, disable and delete it, shown only when it is |

Edits on the config screen are not saved until you press `w`. A yellow
`unsaved` marker sits next to the heading until you do, and `q` asks once
before throwing the work away. A failed write shows up as an error in the
status bar, not as a silent success.

The UI does not poll. It holds one connection to the daemon, which pushes a
fresh snapshot every time a device, the log or the config changes, so a switch
shows up the moment it happens. If the daemon goes away the status bar reads
`daemon offline` and the UI reconnects on its own once it is back.

## Configuration

`~/.config/poweraudio/config.toml`, created on first run. Honours
`XDG_CONFIG_HOME`.

```toml
[general]
backend = "auto"            # kept for older files, every value means pactl
log_level = "info"          # debug, info, warn, error
# log_file = "/home/you/.local/state/poweraudio/daemon.log"

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

The daemon reloads the file on its own when it changes on disk, checking the
modification time every two seconds, so editing by hand needs no restart. A
file that fails to parse is logged and ignored, and the running config stays.
`poweraudio reload` forces a read right away.

`on_connect` decides what a Bluetooth device connecting is allowed to do.
`always` takes the output every time. `priority` only takes it when the new
device outranks whatever is playing, so plugging in earbuds while your USB
headset is on the list above them changes nothing. Two devices that are both
off the list tie, so `priority` with an empty ranking never switches; the UI
says so next to the option. `never` leaves the switching to you and keeps the
daemon around for the event log and the UI.

`on_disconnect` picks the fallback. `priority` walks your ranking top down and
takes the first device that can play. `previous` returns to whatever was
default before the daemon switched away, and falls through to the ranking when
that device has gone or its port can no longer play.

`switch_delay_ms` is the head start you give PipeWire to register the new sink
before the daemon goes looking for it. Raise it if your adapter is slow, though
the retries cover most of that already.

`log_level` is the lowest level written to stderr, which systemd captures. The
in-memory log the UI shows keeps every level regardless. `log_file` appends the
same lines to a file, useful for a daemon that was not started by systemd.

`socket_path` defaults to `$XDG_RUNTIME_DIR/poweraudio.sock`. The socket is
created with mode 0600.

`tui.show_volume` exists in the file and does nothing. The UI always draws
volume bars.

Pressing `w` in the UI rewrites the whole file from the daemon's in-memory
config, so comments you added by hand do not survive. Edit the file directly if
you want to keep them; the daemon picks the change up within two seconds.

## Reading the logs

The daemon keeps its last 200 events in memory. The status screen shows them
newest first with a date, coloured by level: debug lines are dimmed, warnings
are amber, failures are red. Sinks appearing and going away are debug lines
and name the device rather than a number. `poweraudio status` prints the last
ten from a shell.

`skipping switch: X is not ranked above Y` means `on_connect` is set to
`priority` and your ranking said no. `giving up waiting for the audio sink of
X` means BlueZ connected but PipeWire never produced a sink, which is usually a
codec or profile problem rather than anything poweraudio can fix.

The same lines go to stderr, so `journalctl --user -u poweraudio -f` works when
the UI is not running.

## How it is put together

One binary, three faces.

`poweraudio daemon` runs the event loop. It holds a D-Bus subscription for
BlueZ property changes, a `pactl subscribe` pipe for sink and default-device
changes, a two second check on the config file's modification time, and a Unix
socket serving newline-delimited JSON requests. It shells out to `pactl` rather
than linking against anything, so there is no cgo and no libpipewire version
to match. Listing sinks costs one small JSON document per refresh; the previous
backend parsed the entire PipeWire object graph, about half a megabyte, to find
four sinks.

Without arguments you get the UI, built on Bubble Tea. It owns no audio state.
It opens one `subscribe` connection and the daemon pushes a snapshot of
devices, status, events and config after every change, coalesced so a burst of
changes is one redraw. Every action is a request back. Which means the UI can
come and go, and a daemon with no UI attached behaves identically.

With a command you get the CLI, which is one snapshot in and at most one
request out, printed plainly.

```
internal/audio       one pactl backend behind the Backend interface
internal/bluetooth   BlueZ D-Bus subscription
internal/priority    the matcher the daemon and the UI share
internal/daemon      event loop, switching rules, IPC server, subscriptions
internal/ipc         wire protocol and client
internal/tui         Bubble Tea screens
internal/cli         subcommands
internal/config      TOML load and save
```

## Troubleshooting

**The UI says the daemon is offline.** Check `systemctl --user status
poweraudio`. If the unit is not installed, press `i` on the status screen. The
UI reconnects on its own once a daemon answers.

**Nothing switches when I connect.** Look at the status screen or run
`poweraudio status`. No `bluetooth connected` line means the D-Bus subscription
never came up, so check that `bluetoothd` is running. A `connected` line with
nothing after it means the sink never appeared, which you can confirm with
`pactl list sinks short` while the device is connected.

**It switches to the wrong thing on disconnect.** Your priority list is either
empty or nothing on it is present, in which case the daemon falls back to the
first sink it can find. Add the devices you actually use on the config screen.

**The unit is inactive but a daemon is running.** A daemon that finds another
one already listening logs `another poweraudio daemon is already listening` and
exits 0. That is deliberate: the unit restarts on failure every five seconds,
and a session daemon holding the socket used to keep it in that loop until
logout. Stop the session daemon and start the unit, or just keep using the one
you have.

## Working on it

```bash
go build ./...
go vet ./...
go test -race ./...
```

The tests cover the parsing that talks to `pactl` and BlueZ, the priority
matching, the config round trip, the IPC server including the subscription
stream, the locking around the daemon's shared state, the config file watch,
every CLI command against a fake client, and the UI's screens at five terminal
sizes. They need no audio server: the backend is stubbed and the samples are
captured output.

## License

MIT
