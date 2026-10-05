# Changelog

This file lists the changes in each poweraudio release. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the version
numbers follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). The
README explains what each kind of version bump means for this project.

Add each change under "Unreleased" in the same pull request that makes it.
`scripts/release.sh` turns that section into the next release.

## [Unreleased]

### Added

- Version numbers. Each release is a `vX.Y.Z` git tag, and this file lists
  what it changed. `poweraudio --version` prints the number of the binary,
  and `make version` prints the number that a build of the checkout would get.
- `poweraudio status` shows the version of the running daemon. It also shows
  a warning when the daemon and the command are different versions. This
  happens after `make install` if you do not restart the service.
- The status screen in the UI shows the version of the daemon in its title.
- `scripts/release.sh` makes a release: it moves the "Unreleased" notes to a
  new version section, commits the change and creates the tag.

### Changed

- The daemon writes its version in the first log line.
- `install.sh` clones the full history without file contents, instead of only
  the last commit. The build must see the release tags to find its version.

### Fixed

- After a restart, the daemon no longer falls back to a Razer Barracuda X
  with the earcups off. The receiver reports the earcups only when they turn
  on or off, so a new daemon did not know that they were off. The daemon now
  saves each report in `$XDG_RUNTIME_DIR/poweraudio-earcups` and reads it at
  start. The file does not survive a reboot.

## [0.4.1] - 2026-10-05

### Fixed

- The fallback and `next` no longer pick an AirPlay speaker, a PulseAudio
  tunnel or another network sink unless the priority list names it. When an
  AirPlay speaker is on the network, PipeWire can add a sink for it every few
  minutes.
- A sink with no description shows its sink name. Before, it showed
  `(null)`.
- Debug lines can use only 50 of the 200 places in the event log. Frequent
  sink changes no longer push switches and warnings out of the log.
- When WirePlumber moves the output away from Bluetooth headphones that
  disconnected, the notification says "The previous output went away". Before,
  it said "Changed outside poweraudio".
- Log lines for added and removed sinks always give the device name. Before,
  some lines gave only a number, for example `sink removed: 942`.

## [0.4.0] - 2026-10-02

### Added

- Fallback when the default output cannot play. This includes a sink whose
  active port has nothing connected, and a Razer Barracuda X with the earcups
  off. The daemon reads the earcup state from the HID reports of the receiver.
  `make install` and `install.sh` install the udev rule that this needs.
- Holds at startup, suspend, resume and shutdown. During these periods, the
  daemon does not fall back. It waits until the sink list stops changing.
  Before, after a resume the output often went to the first device that came
  back, which was frequently HDMI.
- An audio stack probe. `status` shows the PipeWire and WirePlumber versions
  and a warning for each part that is missing.
- The `--notify` option for `set` and `next`, to use with keyboard shortcuts.

### Changed

- Notifications go through D-Bus instead of `notify-send`, and each one
  replaces the previous one. No notifications show at login or while the
  computer sleeps.
- The fallback and `next` skip "Dummy Output". They also skip virtual sinks,
  for example EasyEffects, unless the priority list names them.
- The daemon ignores Bluetooth devices that have no audio profile, for example
  a mouse or a keyboard.
- The README is shorter and uses simpler language.

## [0.3.0] - 2026-09-18

### Added

- The command line tool, with the commands `list`, `status`, `set`, `next`,
  `volume`, `mute`, `watch` and `reload`. `list`, `status` and `watch` accept
  `--json`.
- The daemon reads the configuration file again within two seconds after the
  file changes.

### Changed

- One backend that uses `pactl` replaces the separate PipeWire and PulseAudio
  backends. PipeWire systems need pipewire-pulse. `wpctl` and `pw-dump` are
  not necessary now.
- The daemon sends updates to the UI as they occur. Before, the UI asked for
  updates on a timer.
- The daemon and the UI use the same priority matching, so they always agree
  about which device an entry matches.

### Fixed

- A second daemon stops cleanly when a different daemon already uses the
  socket. Before, systemd started it again every five seconds.

## [0.2.0] - 2026-09-05

### Changed

- New layout for the device, priority, status and setup screens of the UI.

### Fixed

- A crash when the daemon read volumes from `pactl`, which gives
  `value_percent` as text such as `"40%"`.
- `install.sh` deleted the build before it installed the build.
- The daemon saves configuration changes to the file that it read at start.
  Before, it always saved to the default path.
- Configuration writes go to a temporary file first, which then replaces the
  configuration file. A crash during a write can no longer leave a partial
  file.
- The daemon combines bursts of sink change events into one update.

## [0.1.0] - 2026-05-26

The first release.

### Added

- A daemon that monitors BlueZ over D-Bus. When Bluetooth headphones connect,
  it moves the output to them. When they disconnect, it moves the output to
  the highest available device on a priority list, or to the previous device.
  If the sink of the headphones appears late, the daemon tries again.
- The settings `on_connect` (`always`, `priority` or `never`) and
  `on_disconnect` (`priority` or `previous`).
- A terminal UI that shows the devices and sets the default output. It also
  changes the volume, mutes devices, edits the priority list and installs the
  systemd user service.
- Desktop notifications through `notify-send`.
- `install.sh` and `uninstall.sh`.

[Unreleased]: https://github.com/roverflow/poweraudio/compare/v0.4.1...HEAD
[0.4.1]: https://github.com/roverflow/poweraudio/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/roverflow/poweraudio/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/roverflow/poweraudio/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/roverflow/poweraudio/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/roverflow/poweraudio/releases/tag/v0.1.0
