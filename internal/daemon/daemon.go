// Package daemon is the long-running half of poweraudio. It watches BlueZ and
// the audio backend, decides where the default output belongs, and serves the
// state a UI needs over a Unix socket. Everything a client can ask for goes
// through Handle or Subscribe; the rest of the package is unexported.
package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/bluetooth"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
	"github.com/roverflow/poweraudio/internal/priority"
)

const (
	// sinkChangeDebounce collapses the burst of change events pactl emits
	// while a volume slider moves. Listing sinks for every one of them meant
	// several subprocesses per volume step.
	sinkChangeDebounce = 200 * time.Millisecond

	// pendingTTL is how long the daemon keeps looking for the audio sink of a
	// device BlueZ has already reported as connected.
	pendingTTL = 15 * time.Second

	// pendingRetryInterval is the safety net behind the sink-added event. If
	// pactl subscribe is working the switch happens on the event instead.
	pendingRetryInterval = 500 * time.Millisecond

	// disconnectSettle gives the sink time to disappear before the daemon
	// decides where the output should go instead.
	disconnectSettle = 300 * time.Millisecond

	// maxEvents is the size of the in-memory log the status screen reads. It
	// keeps every level, so a debug line still costs a slot.
	maxEvents = 200
)

// configPollInterval is how often the config file's mtime is checked. It is a
// variable so tests do not have to wait two seconds for a reload.
var configPollInterval = 2 * time.Second

// pendingBT is a Bluetooth device that has connected but whose audio sink
// PipeWire has not published yet.
type pendingBT struct {
	mac    string
	name   string
	expiry time.Time
}

type Daemon struct {
	backend   audio.Backend
	btMonitor *bluetooth.Monitor

	// configPath is where this daemon was told to read its config, so saves
	// from the UI land in the same file rather than always in the default one.
	configPath string
	startTime  time.Time

	mu            sync.RWMutex
	cfg           config.Config
	devices       []audio.Device
	previousID    string
	lastDefaultID string
	events        []ipc.EventLog
	pending       *pendingBT
	// ownSwitchID is the sink this daemon just selected. The matching event
	// from pactl arrives afterwards, and without this the daemon announces its
	// own switch a second time.
	ownSwitchID string

	// switchMu serializes the multi-step switch sequences. Each one reads the
	// sink list, decides, and sets a default; two interleaving would leave the
	// output somewhere neither of them chose.
	switchMu sync.Mutex

	// saveMu serializes writes to the config file. Requests are handled on the
	// connection's own goroutine now, so two clients pressing save at the same
	// moment would otherwise race over the same temporary file.
	saveMu sync.Mutex
	// ownSaveMod is the mtime left by this daemon's last save. The config
	// watcher compares against it so a save from the UI does not come back as
	// an external edit and reload the file the daemon just wrote.
	ownSaveMod time.Time

	logMu   sync.Mutex
	logFile *os.File
	logPath string

	subMu sync.RWMutex
	subs  map[*subscriber]struct{}
}

func New(cfg config.Config, backend audio.Backend, configPath string) *Daemon {
	return &Daemon{
		cfg:        cfg,
		backend:    backend,
		configPath: configPath,
		startTime:  time.Now(),
		subs:       make(map[*subscriber]struct{}),
	}
}

// Run holds the event loop until ctx ends. IPC requests do not come through
// here: the server calls Handle and Subscribe directly, because serving them
// from this goroutine meant a held volume key queued behind a sink refresh.
func (d *Daemon) Run(ctx context.Context) error {
	d.setLogFile(d.Config().General.LogFile)
	defer d.closeLogFile()

	d.infof("daemon started with %s backend", d.backend.Name())

	d.refreshDevices(ctx)

	var btEvents <-chan bluetooth.Event
	btMon, err := bluetooth.NewMonitor()
	if err != nil {
		d.warnf("bluetooth monitoring unavailable: %v", err)
	} else {
		d.btMonitor = btMon
		defer btMon.Close()
		ch, err := btMon.Subscribe(ctx)
		if err != nil {
			d.warnf("bluetooth monitoring unavailable: %v", err)
		} else {
			btEvents = ch
			d.infof("bluetooth monitoring active")
		}
	}

	audioEvents, err := d.backend.SubscribeEvents(ctx)
	if err != nil {
		d.errorf("audio event subscription failed: %v", err)
	}

	d.runEvents(ctx, btEvents, audioEvents)

	d.infof("daemon stopping")
	return ctx.Err()
}

// runEvents owns everything that touches the audio backend on a timer: the
// Bluetooth handlers, the debounced sink refresh, the retry that waits for a
// Bluetooth sink to appear, and the config file watch.
func (d *Daemon) runEvents(ctx context.Context, btEvents <-chan bluetooth.Event, audioEvents <-chan audio.Event) {
	var (
		settle <-chan time.Time // a burst of sink changes is still arriving
		retry  <-chan time.Time // a Bluetooth sink has not turned up yet
	)

	armRetry := func() <-chan time.Time {
		if d.hasPending() {
			return time.After(pendingRetryInterval)
		}
		return nil
	}

	poll := time.NewTicker(configPollInterval)
	defer poll.Stop()
	lastMod := d.configModTime()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-btEvents:
			if !ok {
				btEvents = nil
				continue
			}
			d.handleBluetoothEvent(ctx, ev)
			retry = armRetry()

		case ev, ok := <-audioEvents:
			if !ok {
				audioEvents = nil
				continue
			}
			if ev.Type == audio.EventSinkChanged {
				if settle == nil {
					settle = time.After(sinkChangeDebounce)
				}
				continue
			}
			d.handleAudioEvent(ctx, ev)
			retry = armRetry()

		case <-settle:
			settle = nil
			d.refreshDevices(ctx)

		case <-retry:
			d.refreshDevices(ctx)
			d.attemptPending(ctx)
			retry = armRetry()

		case <-poll.C:
			lastMod = d.checkConfigFile(lastMod)
		}
	}
}

// checkConfigFile reloads the config when the file changed underneath the
// daemon, so editing it by hand takes effect without a restart. It returns the
// mtime to compare against next time.
func (d *Daemon) checkConfigFile(lastMod time.Time) time.Time {
	mod := d.configModTime()
	if mod.IsZero() || mod.Equal(lastMod) {
		return lastMod
	}

	d.saveMu.Lock()
	own := d.ownSaveMod
	d.saveMu.Unlock()
	if mod.Equal(own) {
		return mod
	}

	path := config.ResolvePath(d.configPath)
	cfg, err := config.Load(d.configPath)
	if err != nil {
		// A half-written or broken file is not a reason to throw away the
		// settings the daemon is already running with.
		d.errorf("reloading config from %s: %v", path, err)
		return mod
	}
	d.applyConfig(cfg)
	d.infof("config reloaded from %s", path)
	return mod
}

func (d *Daemon) configModTime() time.Time {
	info, err := os.Stat(config.ResolvePath(d.configPath))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// applyConfig swaps in a whole config and tells subscribers. The log file can
// move with it, so the destination is reopened here rather than only at start.
func (d *Daemon) applyConfig(cfg config.Config) {
	d.mu.Lock()
	d.cfg = cfg
	d.mu.Unlock()
	d.setLogFile(cfg.General.LogFile)
	d.changed()
}

func (d *Daemon) handleBluetoothEvent(ctx context.Context, ev bluetooth.Event) {
	if ev.Connected {
		d.infof("bluetooth connected: %s (%s)", ev.DeviceName, ev.MACAddress)

		// BlueZ reports the link before PipeWire publishes the sink, so give
		// it a head start before going to look.
		if !sleepCtx(ctx, time.Duration(d.switching().SwitchDelayMs)*time.Millisecond) {
			return
		}
		d.refreshDevices(ctx)

		if d.switching().OnConnect == "never" {
			return
		}
		if d.trySwitchToBT(ctx, ev.MACAddress, ev.DeviceName) {
			return
		}

		d.infof("bluetooth device connected but has no audio sink yet, waiting")
		d.mu.Lock()
		d.pending = &pendingBT{
			mac:    ev.MACAddress,
			name:   ev.DeviceName,
			expiry: time.Now().Add(pendingTTL),
		}
		d.mu.Unlock()
		return
	}

	d.infof("bluetooth disconnected: %s (%s)", ev.DeviceName, ev.MACAddress)

	d.mu.Lock()
	// Only the device that is being waited on cancels the wait. Clearing it
	// for any disconnect meant an unrelated headset going away made the daemon
	// forget the one that was still trying to arrive.
	if d.pending != nil && strings.EqualFold(d.pending.mac, ev.MACAddress) {
		d.pending = nil
	}
	wasDefault := d.lastDefaultID
	d.mu.Unlock()

	if !sleepCtx(ctx, disconnectSettle) {
		return
	}
	d.refreshDevices(ctx)

	// Only step in when the sink that was playing actually went away.
	// Disconnecting an idle second headset used to move the output off the
	// device you were listening on.
	if d.hasDevice(wasDefault) {
		return
	}
	d.fallback(ctx)
}

func (d *Daemon) handleAudioEvent(ctx context.Context, ev audio.Event) {
	switch ev.Type {
	case audio.EventSinkAdded:
		before := d.GetDevices()
		d.refreshDevices(ctx)
		d.logSinkDiff("sink added", d.GetDevices(), before, ev.DeviceID)
		d.attemptPending(ctx)

	case audio.EventSinkRemoved:
		before := d.GetDevices()
		d.refreshDevices(ctx)
		d.logSinkDiff("sink removed", before, d.GetDevices(), ev.DeviceID)

	case audio.EventDefaultChanged:
		d.mu.RLock()
		previous := d.lastDefaultID
		d.mu.RUnlock()

		d.refreshDevices(ctx)

		d.mu.RLock()
		current := d.lastDefaultID
		d.mu.RUnlock()

		// Take the claim on the first change event that arrives, whether or
		// not it matches. One claim covers exactly one observed change, so a
		// stale one cannot silence an unrelated switch later on.
		ours := d.takeOwnSwitch(current)

		if current == previous || ours {
			return
		}
		if !d.notifications().OnDeviceChange {
			return
		}
		name := d.deviceName(current)
		d.infof("default device changed to %s", name)
		d.notify("Default Device Changed", fmt.Sprintf("Now playing through %s", name))
	}
}

// logSinkDiff names the sinks that appeared in one list and not the other. The
// pactl event carries only the pulse index, which a sink list keyed by name
// cannot be matched against, so the journal used to fill with lines like
// "sink added: 1648" that said nothing about which device it was.
func (d *Daemon) logSinkDiff(what string, have, missing []audio.Device, rawID string) {
	seen := make(map[string]struct{}, len(missing))
	for _, dev := range missing {
		seen[dev.ID] = struct{}{}
	}
	var names []string
	for _, dev := range have {
		if _, ok := seen[dev.ID]; ok {
			continue
		}
		name := dev.Name
		if name == "" {
			name = dev.ID
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		d.debugf("%s: %s", what, rawID)
		return
	}
	d.debugf("%s: %s", what, strings.Join(names, ", "))
}

// trySwitchToBT finds the sink belonging to a connected Bluetooth device and
// makes it the default, honouring on_connect. It reports whether the sink
// existed, so callers know whether there is any point waiting longer. A switch
// declined on priority grounds still counts: the sink is there, the answer is
// just no.
func (d *Daemon) trySwitchToBT(ctx context.Context, mac, name string) bool {
	d.switchMu.Lock()
	defer d.switchMu.Unlock()

	devices := d.GetDevices()
	dev := findBTDevice(devices, mac, name)
	if dev == nil {
		return false
	}

	if d.switching().OnConnect == "priority" {
		// The default is already cached from the last refresh, so asking the
		// backend again would re-list every sink for an answer we hold.
		if current := deviceByID(devices, d.defaultID()); current != nil {
			entries := d.priorities()
			newRank := priority.Rank(*dev, entries)
			currentRank := priority.Rank(*current, entries)
			if newRank >= currentRank {
				d.warnf("%s", skipReason(*dev, *current, newRank, currentRank, len(entries)))
				return true
			}
		}
	}

	d.savePrevious()
	if err := d.setDefault(ctx, dev.ID); err != nil {
		d.errorf("switch failed: %v", err)
		return true
	}
	d.infof("switched to %s", dev.Name)
	d.notify("Audio Switched", fmt.Sprintf("Now playing through %s", dev.Name))
	return true
}

// skipReason says why a connect did not take the output. "Lower priority" was
// misleading when neither device was on the list at all, which is the common
// case for a ranking with one entry in it.
func skipReason(next, current audio.Device, nextRank, currentRank, entries int) string {
	if nextRank >= entries && currentRank >= entries {
		return fmt.Sprintf("skipping switch: neither %s nor %s is on the priority list", next.Name, current.Name)
	}
	return fmt.Sprintf("skipping switch: %s is not ranked above %s", next.Name, current.Name)
}

// fallback picks where the output goes once the device you were listening on
// has gone away.
func (d *Daemon) fallback(ctx context.Context) {
	d.switchMu.Lock()
	defer d.switchMu.Unlock()

	devices := d.GetDevices()

	var target *audio.Device
	if d.switching().OnDisconnect == "previous" {
		d.mu.RLock()
		previousID := d.previousID
		d.mu.RUnlock()

		for i := range devices {
			if devices[i].ID == previousID && devices[i].Available {
				target = &devices[i]
				break
			}
		}
	}
	// Either the ranking was asked for, or the device that was playing before
	// is gone too. Leaving the output wherever the session happened to put it
	// is the behaviour this daemon exists to avoid.
	if target == nil {
		target = priority.Best(devices, d.priorities())
	}
	if target == nil {
		d.warnf("no fallback device available")
		return
	}

	if err := d.setDefault(ctx, target.ID); err != nil {
		d.errorf("fallback switch failed: %v", err)
		return
	}
	d.infof("fallback to %s", target.Name)
	d.notify("Audio Fallback", fmt.Sprintf("Switched to %s", target.Name))
}

// attemptPending retries the switch for a Bluetooth device that connected
// before its sink existed. It reads the cached device list, so callers refresh
// first.
func (d *Daemon) attemptPending(ctx context.Context) {
	d.mu.RLock()
	p := d.pending
	d.mu.RUnlock()

	if p == nil {
		return
	}
	if time.Now().After(p.expiry) {
		d.clearPending(p)
		d.warnf("giving up waiting for the audio sink of %s", p.name)
		return
	}
	if d.switching().OnConnect == "never" {
		d.clearPending(p)
		return
	}

	if d.trySwitchToBT(ctx, p.mac, p.name) {
		d.clearPending(p)
	}
}

// setDefault switches the output and records the choice, so the event pactl
// sends back is recognised as this daemon's own doing. Callers already holding
// switchMu use this; SetDefault takes the lock for them.
func (d *Daemon) setDefault(ctx context.Context, deviceID string) error {
	d.mu.Lock()
	d.ownSwitchID = deviceID
	d.mu.Unlock()

	if err := d.backend.SetDefaultSink(ctx, deviceID); err != nil {
		d.mu.Lock()
		d.ownSwitchID = ""
		d.mu.Unlock()
		return err
	}

	d.refreshDevices(ctx)
	return nil
}

// SetDefault is the manual switch behind the UI. It waits on the same lock the
// automatic switches use, so a keypress and a Bluetooth connect landing at the
// same moment cannot leave the output somewhere neither of them picked.
func (d *Daemon) SetDefault(ctx context.Context, deviceID string) error {
	d.switchMu.Lock()
	defer d.switchMu.Unlock()
	return d.setDefault(ctx, deviceID)
}

// takeOwnSwitch reports whether id is the change this daemon just made, and
// clears the claim either way.
func (d *Daemon) takeOwnSwitch(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	claimed := d.ownSwitchID
	d.ownSwitchID = ""
	return id != "" && id == claimed
}

// hasDevice reports whether id is still in the sink list.
func (d *Daemon) hasDevice(id string) bool {
	if id == "" {
		return false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, dev := range d.devices {
		if dev.ID == id {
			return true
		}
	}
	return false
}

func (d *Daemon) hasPending() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.pending != nil
}

// clearPending drops p only if it is still the attempt in flight, so a newer
// connection is not thrown away by an older one finishing late.
func (d *Daemon) clearPending(p *pendingBT) {
	d.mu.Lock()
	if d.pending == p {
		d.pending = nil
	}
	d.mu.Unlock()
}

func (d *Daemon) refreshDevices(ctx context.Context) {
	devices, err := d.backend.ListSinks(ctx)
	if err != nil {
		d.errorf("refreshing devices failed: %v", err)
		return
	}
	d.mu.Lock()
	d.devices = devices
	for _, dev := range devices {
		if dev.IsDefault {
			d.lastDefaultID = dev.ID
			break
		}
	}
	d.mu.Unlock()
	d.changed()
}

// savePrevious remembers where the output was before a switch, for the
// previous fallback mode. The cached default is what the last refresh saw, and
// every caller refreshes first.
func (d *Daemon) savePrevious() {
	d.mu.Lock()
	d.previousID = d.lastDefaultID
	d.mu.Unlock()
}

func (d *Daemon) GetDevices() []audio.Device {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]audio.Device, len(d.devices))
	copy(out, d.devices)
	return out
}

func (d *Daemon) StartTime() time.Time {
	return d.startTime
}

// ConfigPath is where this daemon reads and writes its config. It is fixed at
// startup, so no lock is needed.
func (d *Daemon) ConfigPath() string {
	return d.configPath
}

func (d *Daemon) Config() config.Config {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return copyConfig(d.cfg)
}

// The event goroutines read config while IPC requests write it, so every read
// outside a locked section goes through one of these.

func (d *Daemon) switching() config.SwitchingConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg.Switching
}

func (d *Daemon) notifications() config.NotificationsConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg.Notifications
}

func (d *Daemon) priorities() []config.PriorityEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]config.PriorityEntry, len(d.cfg.Priority))
	copy(out, d.cfg.Priority)
	return out
}

func (d *Daemon) defaultID() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastDefaultID
}

func (d *Daemon) deviceName(id string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, dev := range d.devices {
		if dev.ID == id {
			return dev.Name
		}
	}
	return id
}

// copyConfig detaches the priority list, so a caller holding a config cannot
// see it change underneath as the UI edits the ranking.
func copyConfig(cfg config.Config) config.Config {
	out := cfg
	out.Priority = make([]config.PriorityEntry, len(cfg.Priority))
	copy(out.Priority, cfg.Priority)
	return out
}

// notify raises a desktop notification. The child is waited on in the
// background: an unreaped notify-send leaves a zombie for the life of the
// daemon, and this runs on every switch.
func (d *Daemon) notify(title, body string) {
	if !d.notifications().Enabled {
		return
	}
	cmd := exec.Command("notify-send", "-a", "poweraudio", "-i", "audio-headphones", title, body)
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

func deviceByID(devices []audio.Device, id string) *audio.Device {
	if id == "" {
		return nil
	}
	for i := range devices {
		if devices[i].ID == id {
			return &devices[i]
		}
	}
	return nil
}

// findBTDevice matches a BlueZ device against the sink list. MAC first, since
// two headsets can share a model name, then the alias for backends that do not
// report a MAC.
func findBTDevice(devices []audio.Device, mac, name string) *audio.Device {
	if mac != "" {
		for i := range devices {
			if devices[i].Type == audio.DeviceTypeBluetooth &&
				devices[i].MACAddress != "" &&
				strings.EqualFold(devices[i].MACAddress, mac) {
				return &devices[i]
			}
		}
	}
	if name == "" {
		return nil
	}
	needle := strings.ToLower(name)
	for i := range devices {
		if devices[i].Type == audio.DeviceTypeBluetooth &&
			strings.Contains(strings.ToLower(devices[i].Name), needle) {
			return &devices[i]
		}
	}
	return nil
}

// sleepCtx waits for d, reporting false when the context was cancelled first
// so callers can stop rather than carry on through a shutdown.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
