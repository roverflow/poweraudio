# poweraudio

poweraudio controls the default audio output on a Linux desktop. When
Bluetooth headphones connect, it moves the output to the headphones. When they
disconnect, it moves the output to the highest device on a list that you
make.

Without poweraudio, many desktops keep the output on the speakers after the
headphones connect. After a disconnect, the output often goes to an HDMI
monitor that has no speakers.

poweraudio has three parts in one binary:

- A daemon that monitors the devices and changes the output.
- A terminal UI to see the devices and edit the list.
- A command line tool for scripts, status bars and keyboard shortcuts.

## Requirements

- Linux with PipeWire and pipewire-pulse, or with PulseAudio.
- `pactl` from PulseAudio 16 or later, or from PipeWire. Ubuntu 22.04 and
  later are satisfactory.
- BlueZ, for Bluetooth headphones.
- A systemd user session, to start the daemon at login.
- Go 1.25 or later, to build.

All parts run as your user. No part needs root access.

## Install

1. Get the source:

   ```bash
   git clone https://github.com/roverflow/poweraudio.git
   cd poweraudio
   ```

   To install a release instead of the newest code, check out its tag, for
   example `git checkout v0.4.1`. [CHANGELOG.md](CHANGELOG.md) lists the
   releases.

2. Build and install the binary and the service file:

   ```bash
   make install
   ```

3. Start the service and enable it at login:

   ```bash
   systemctl --user daemon-reload
   systemctl --user enable --now poweraudio
   ```

4. Make sure that the daemon runs:

   ```bash
   poweraudio status
   ```

   The `version` line shows the version of the daemon.

NOTE: The Razer Barracuda X receiver needs a udev rule. `make install` installs
it if sudo is available without a password. If not, install it manually:

```bash
sudo install -Dm644 configs/70-poweraudio.rules /etc/udev/rules.d/70-poweraudio.rules
sudo udevadm control --reload-rules && sudo udevadm trigger --subsystem-match=hidraw
```

## Use

Run `poweraudio` without a command to open the terminal UI. Press `?` in the UI
to see all keys.

| Command | Function |
|---------|----------|
| `list [--json]` | Shows the output devices. A `*` marks the default. |
| `status [--json]` | Shows the default device, the sound system, uptime and recent events. |
| `set <query> [--notify]` | Makes a device the default output. |
| `next [--notify]` | Changes to the next device that can play. |
| `volume <+N\|-N\|N> [--device Q]` | Sets the volume, from 0 to 150 percent. |
| `mute [--device Q]` | Mutes or unmutes a device. |
| `watch [--json]` | Shows one line for each change, for a status bar. |
| `reload` | Makes the daemon read the configuration file again. |
| `daemon` | Runs the daemon in the foreground. |

A query is part of the device name, sink name or MAC address. Case is not
important. An exact match has priority. If a query matches more than one
device, the command stops and shows the matches.

Use `--notify` when you bind `next` or `set` to a key. The daemon then shows a
desktop notification with the new output.

Exit codes: 0 is success, 2 is an incorrect command line, and 1 is all other
errors. For example, the daemon does not run or no device matches.

## Configuration

The configuration file is `~/.config/poweraudio/config.toml`. The daemon makes
it at the first start. The daemon reads the file again within two seconds of a
change.

```toml
[switching]
on_connect = "always"       # always, priority, never
on_disconnect = "priority"  # priority, previous
switch_delay_ms = 500

[notifications]
enabled = true
on_device_change = true

[[priority]]
match = "JBL Tune 520BT"
type = "bluetooth"

[[priority]]
match = "Built-in Audio Analog Stereo"
```

| Setting | Function |
|---------|----------|
| `on_connect` | `always` moves the output to each Bluetooth device that connects. `priority` moves it only if the new device is higher on the list. `never` does not move it. |
| `on_disconnect` | `priority` moves the output to the highest device on the list that can play. `previous` moves it back to the device before the headphones. |
| `switch_delay_ms` | Time in milliseconds to wait for PipeWire to make the headphone output after the connection. |
| `notifications.enabled` | Turns desktop notifications on or off. |
| `notifications.on_device_change` | Shows a notification when a different program changes the output. |
| `[[priority]]` | The device list. The first entry has the highest priority. `match` is part of the device name, sink name or MAC address. `type` is optional. |

You can also edit the list in the UI, on the config screen.

NOTE: When you save from the UI, the daemon writes the full file again. The
daemon does not keep comments that you added. To keep comments, edit the file
in a text editor.

## Operation

On a connection, the daemon waits for PipeWire to make the headphone output.
Then it makes that output the default. It ignores Bluetooth devices that have
no audio output, for example a mouse or a keyboard.

On a disconnect, the daemon moves the output only if the current output cannot
play. A device cannot play if it is gone, if its port has nothing connected,
or if it is a Barracuda X with the earcups off. The daemon never selects
"Dummy Output". It selects a virtual sink only if the list includes it.

At startup, after a resume and during a shutdown, devices go and come back
over some seconds. During these periods the daemon does not move the output to
a fallback device. Headphones that connect still get the output. After startup
or resume, the daemon checks the output one time when the devices are stable.

Notifications replace the previous notification. The daemon does not show
notifications at login or while the computer sleeps.

## Troubleshooting

| Symptom | Possible cause | Action |
|---------|----------------|--------|
| The UI shows `daemon offline`. | The service does not run. | Run `systemctl --user status poweraudio`. Press `i` on the status screen to install the service. |
| The output does not change when the headphones connect. | `bluetoothd` does not run, or PipeWire did not make an output. | Run `poweraudio status` and look for `bluetooth connected`. Run `pactl list sinks short` to see the outputs. |
| The log shows `giving up waiting for the audio sink`. | PipeWire did not make an output in 15 seconds. | Examine the Bluetooth profile and codec of the headphones. |
| The output goes to an incorrect device after a disconnect. | The list is empty, or no device on the list can play. | Add your devices to the list on the config screen. |
| `status` shows a warning line. | The daemon cannot use a part of the sound system. | Do the action that the warning gives. |

To see the log, run `journalctl --user -u poweraudio -f`.

## Update

```bash
git pull
make install
systemctl --user restart poweraudio
```

If you do not restart the service, the old daemon continues to run.
`poweraudio status` then shows a warning that the daemon and the command are
different versions.

## Versions

poweraudio uses version numbers in the form x.y.z. Each release has a git tag
`vx.y.z`. [CHANGELOG.md](CHANGELOG.md) tells what each release changed. To see
the version of the binary, run `poweraudio --version`.

A build that is not a release has a longer version. `0.4.1-3-gabc1234` is three
commits after 0.4.1. `-dirty` means that the build included changes that were
not committed.

Before 1.0.0, the numbers have these meanings:

- A change to y, for example 0.4.1 to 0.5.0, adds features or changes
  behavior. The behavior can be different for the configuration file, the
  commands, the `--json` output or the exit codes. The changelog tells what
  to do.
- A change to z, for example 0.4.0 to 0.4.1, only fixes bugs.

After 1.0.0, only a change to x can make an incompatible change to the
configuration file, the commands, the `--json` output or the exit codes.

## Remove

```bash
./uninstall.sh          # Stops the daemon. Removes the binary, service file and socket.
./uninstall.sh --purge  # Also removes ~/.config/poweraudio.
```

## Development

```bash
go build ./...
go vet ./...
go test -race ./...
```

The tests do not need an audio server.

Add each change to the "Unreleased" section of [CHANGELOG.md](CHANGELOG.md) in
the same pull request. To make a release, run the release script on `main`:

```bash
scripts/release.sh 0.5.0
git push origin main v0.5.0
```

The script runs the tests, moves the "Unreleased" notes to a section for the
new version and commits the change. Then it creates the tag. It does not push.

## License

MIT
