// Package daemon watches BlueZ and the audio backend, decides where audio
// output goes, and serves its state to UIs over a Unix socket.
package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/bluetooth"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
	"github.com/roverflow/poweraudio/internal/notify"
	"github.com/roverflow/poweraudio/internal/power"
	"github.com/roverflow/poweraudio/internal/probe"
	"github.com/roverflow/poweraudio/internal/razer"
	"github.com/roverflow/poweraudio/internal/version"
)

const (
	// sinkChangeDebounce collapses pactl's change burst during a volume drag.
	sinkChangeDebounce = 200 * time.Millisecond

	// pendingTTL bounds the wait for a connected Bluetooth device's sink.
	pendingTTL = 15 * time.Second

	// pendingRetryInterval polls in case the sink-added event never arrives.
	pendingRetryInterval = 500 * time.Millisecond

	disconnectSettle = 300 * time.Millisecond

	maxEvents = 200

	// maxDebugEvents caps debug lines so sink churn cannot push out the rest.
	maxDebugEvents = 50

	// Until the login session is active, the udev rule has not granted access
	// to the Barracuda node, so open failures this early are expected.
	quietStart = 30 * time.Second
)

// Holds stop the daemon falling back while sinks are still arriving at boot
// or after a resume. A hold ends once the sink list is quiet for its stable
// time or reaches its cap. They are variables so tests can shorten them.
var (
	holdStable       = 1500 * time.Millisecond
	holdStartupMax   = 10 * time.Second
	holdResumeStable = 2 * time.Second
	holdResumeMax    = 15 * time.Second

	resubscribeMin = time.Second
	resubscribeMax = 30 * time.Second
)

var configPollInterval = 2 * time.Second

type Daemon struct {
	backend audio.Backend

	configPath string
	startTime  time.Time

	mu            sync.RWMutex
	cfg           config.Config
	devices       []audio.Device
	lastDefaultID string
	events        []ipc.EventLog
	// claim is the switch in flight, so its announcement names the reason.
	claim    *claim
	recentBT map[string]time.Time
	goneBT   map[string]time.Time
	listed   bool
	holding  bool
	audio    probe.Report
	notes    notifier

	// The dongle sends a power report only when the state changes, so the
	// earcup state is unknown until the first report.
	earcupsKnown bool
	earcupsOn    bool
	earcupsFile  string

	switcher switcher
	prober   func(context.Context) probe.Report

	// switchMu serializes switch sequences. Each reads the sink list, decides,
	// and sets a default, and two interleaved ones land where neither chose.
	switchMu sync.Mutex

	// saveMu serializes config writes from concurrent IPC requests.
	saveMu sync.Mutex
	// ownSaveMod lets the config watcher skip the daemon's own saves.
	ownSaveMod time.Time

	logMu   sync.Mutex
	logFile *os.File
	logPath string

	subMu sync.RWMutex
	subs  map[*subscriber]struct{}
}

func New(cfg config.Config, backend audio.Backend, configPath string) *Daemon {
	d := &Daemon{
		cfg:        cfg,
		backend:    backend,
		configPath: configPath,
		startTime:  time.Now(),
		recentBT:   make(map[string]time.Time),
		goneBT:     make(map[string]time.Time),
		subs:       make(map[*subscriber]struct{}),
		prober: func(ctx context.Context) probe.Report {
			return probe.Run(ctx, probe.Exec)
		},
	}
	d.switcher = newPactlSwitcher(d)
	return d
}

// sources holds the event streams. A nil channel is one this machine lacks.
type sources struct {
	bt    <-chan bluetooth.Event
	audio <-chan audio.Event
	link  <-chan razer.Event
	power <-chan power.State
}

// Run runs the event loop until ctx ends. IPC requests do not pass through
// it, so a held volume key never queues behind a sink refresh.
func (d *Daemon) Run(ctx context.Context) error {
	d.setLogFile(d.Config().General.LogFile)
	defer d.closeLogFile()

	d.mu.Lock()
	if d.notes == nil {
		n := notify.New(d.debugf)
		defer n.Close()
		d.notes = n
	}
	d.mu.Unlock()

	d.infof("daemon %s started with %s backend, %s switching", version.String(), d.backend.Name(), d.switcher.name())
	d.rememberEarcups(filepath.Join(config.RuntimeDir(), "poweraudio-earcups"))
	go d.reprobe(ctx)

	var src sources
	btMon, err := bluetooth.NewMonitor()
	if err != nil {
		d.warnf("bluetooth monitoring unavailable: %v", err)
	} else {
		defer btMon.Close()
		if ch, err := btMon.Subscribe(ctx); err != nil {
			d.warnf("bluetooth monitoring unavailable: %v", err)
		} else {
			src.bt = ch
			d.infof("bluetooth monitoring active")
		}
	}

	if ch, err := power.Watch(ctx); err != nil {
		d.debugf("suspend, resume and shutdown will not be noticed: %v", err)
	} else {
		src.power = ch
	}

	src.link = razer.Watch(ctx)

	d.runEvents(ctx, src)

	d.infof("daemon stopping")
	return ctx.Err()
}

// reprobe runs again after the sound server restarts, which applies upgrades.
func (d *Daemon) reprobe(ctx context.Context) {
	r := d.prober(ctx)
	if ctx.Err() != nil {
		return
	}
	d.mu.Lock()
	d.audio = r
	d.mu.Unlock()
	d.infof("audio stack: %s (%s)", r.Summary(), r.Tier())
	for _, p := range r.Problems {
		d.warnf("%s", p)
	}
	d.changed()
}

// runEvents owns every timer that touches the audio backend.
func (d *Daemon) runEvents(ctx context.Context, src sources) {
	var (
		settle <-chan time.Time // a burst of sink changes is still arriving
		retry  <-chan time.Time // a Bluetooth sink has not turned up yet
		resub  <-chan time.Time // time to follow pactl subscribe again

		hold      <-chan time.Time // the sink list has been quiet long enough
		holdEnd   time.Time        // the hold's cap
		holdQuiet time.Duration    // how long the list must stay quiet

		away       = power.Awake
		resumeFrom string // the default when the machine started to go away

		backoff     = resubscribeMin
		streamStart time.Time
		lostStream  bool
	)

	armRetry := func() <-chan time.Time {
		if d.switcher.waiting() {
			return time.After(pendingRetryInterval)
		}
		return nil
	}

	startHold := func(quiet, max time.Duration) {
		d.setHolding(true)
		holdQuiet = quiet
		holdEnd = time.Now().Add(max)
		hold = time.After(quiet)
	}
	extendHold := func() {
		if hold == nil {
			return
		}
		wait := min(holdQuiet, time.Until(holdEnd))
		hold = time.After(max(wait, 0))
	}

	subscribe := func() {
		ch, err := d.backend.SubscribeEvents(ctx)
		if err != nil {
			if !lostStream {
				d.warnf("cannot follow audio events: %v", err)
			}
			lostStream = true
			resub = time.After(backoff)
			backoff = min(backoff*2, resubscribeMax)
			return
		}
		src.audio = ch
		streamStart = time.Now()
	}

	startHold(holdStable, holdStartupMax)
	// Tests pass a stream in. Run passes none, so follow the backend's own.
	if src.audio == nil {
		subscribe()
	} else {
		streamStart = time.Now()
	}
	d.refreshDevices(ctx)

	poll := time.NewTicker(configPollInterval)
	defer poll.Stop()
	lastMod := d.configModTime()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-src.bt:
			if !ok {
				src.bt = nil
				continue
			}
			d.handleBluetoothEvent(ctx, ev)
			retry = armRetry()

		case ev, ok := <-src.audio:
			if !ok {
				// pactl subscribe exits when the sound server restarts.
				// Without a resubscribe the daemon would miss every unplug.
				src.audio = nil
				if ctx.Err() != nil {
					continue
				}
				if time.Since(streamStart) > resubscribeMax {
					backoff = resubscribeMin
				}
				if !lostStream {
					d.warnf("lost the audio event stream, reconnecting")
				}
				lostStream = true
				resub = time.After(backoff)
				backoff = min(backoff*2, resubscribeMax)
				continue
			}
			if lostStream {
				// The first event on a new stream proves the server is back.
				lostStream = false
				d.infof("audio event stream reconnected")
				startHold(holdStable, holdStartupMax)
				d.refreshDevices(ctx)
				go d.reprobe(ctx)
			}
			switch ev.Type {
			case audio.EventSinkChanged:
				if settle == nil {
					settle = time.After(sinkChangeDebounce)
				}
				continue
			case audio.EventSinkAdded, audio.EventSinkRemoved:
				extendHold()
			}
			d.handleAudioEvent(ctx, ev)
			retry = armRetry()

		case <-resub:
			// Reread the sinks on the new stream's first event. Reading
			// here would log a failure on every attempt while the
			// server is down.
			resub = nil
			subscribe()

		case <-settle:
			settle = nil
			d.refreshSettled(ctx)

		case <-retry:
			d.refreshSettled(ctx)
			d.switcher.retry(ctx)
			retry = armRetry()

		case <-hold:
			hold = nil
			if away != power.Awake {
				continue
			}
			d.endHold(ctx, resumeFrom)
			resumeFrom = ""
			retry = armRetry()

		case state, ok := <-src.power:
			if !ok {
				src.power = nil
				continue
			}
			if state != power.Awake {
				if resumeFrom == "" {
					resumeFrom = d.defaultID()
				}
				away = state
				d.setHolding(true)
				hold = nil
				if state == power.ShuttingDown {
					d.infof("system shutting down, holding switches")
				} else {
					d.infof("system going to sleep, holding switches until it resumes")
				}
				continue
			}
			if away == power.Awake {
				continue
			}
			if away == power.ShuttingDown {
				d.infof("shutdown cancelled, waiting for outputs to settle")
			} else {
				d.infof("system resumed, waiting for outputs to come back")
			}
			away = power.Awake
			startHold(holdResumeStable, holdResumeMax)

		case ev, ok := <-src.link:
			if !ok {
				src.link = nil
				continue
			}
			d.handleLinkEvent(ctx, ev)

		case <-poll.C:
			lastMod = d.checkConfigFile(lastMod)
		}
	}
}

// endHold resumes switching. before is the default from before a sleep or
// cancelled shutdown. It is empty after a startup hold, which needs no notice.
func (d *Daemon) endHold(ctx context.Context, before string) {
	d.refreshDevices(ctx)
	d.setHolding(false)
	d.switcher.settled(ctx, "")

	now := d.defaultID()
	d.debugf("hold over, playing on %s", d.deviceName(now))
	if before != "" && now != before {
		d.announce(deviceByID(d.GetDevices(), now), claim{id: now, reason: reasonResume})
	}
}

func (d *Daemon) handleLinkEvent(ctx context.Context, ev razer.Event) {
	switch {
	case ev.Err != nil:
		// Before login the node is root-only and the watcher keeps retrying.
		if time.Since(d.startTime) < quietStart {
			d.debugf("Barracuda earcup watch: %v", ev.Err)
		} else {
			d.warnf("Barracuda earcup watch: %v", ev.Err)
		}
	case ev.Opened:
		d.infof("watching Barracuda earcups on %s", ev.Path)
	default:
		if ev.On {
			d.infof("Barracuda earcups on")
		} else {
			d.infof("Barracuda earcups off")
		}
		d.handleEarcups(ctx, ev.On)
	}
}

// checkConfigFile reloads a hand-edited config and returns the mtime it saw.
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
		// A broken or half-written file keeps the running settings.
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

func (d *Daemon) applyConfig(cfg config.Config) {
	d.mu.Lock()
	d.cfg = cfg
	d.mu.Unlock()
	d.setLogFile(cfg.General.LogFile)
	d.changed()
}

func (d *Daemon) handleBluetoothEvent(ctx context.Context, ev bluetooth.Event) {
	state := "disconnected"
	if ev.Connected {
		state = "connected"
	}
	// Non-audio devices never get a sink, so do not wait 15 seconds for one.
	if ev.NotAudio {
		d.debugf("bluetooth %s: %s (%s), not an audio device", state, ev.DeviceName, ev.MACAddress)
		return
	}
	d.infof("bluetooth %s: %s (%s)", state, ev.DeviceName, ev.MACAddress)

	if ev.Connected {
		// Read before anything reacts, so it names what was playing.
		before := d.defaultID()
		d.noteConnect(ev.MACAddress)

		// BlueZ reports the link before PipeWire publishes the sink.
		if !sleepCtx(ctx, time.Duration(d.switching().SwitchDelayMs)*time.Millisecond) {
			return
		}
		d.refreshSettled(ctx)
		d.switcher.connected(ctx, ev.MACAddress, ev.DeviceName, before)
		return
	}

	d.noteDisconnect(ev.MACAddress)
	d.switcher.disconnected(ctx, ev.MACAddress)
	if !sleepCtx(ctx, disconnectSettle) {
		return
	}
	// refreshSettled moves the output only if the playing sink left, so an
	// idle second headset disconnecting changes nothing.
	d.refreshSettled(ctx)
}

func (d *Daemon) handleAudioEvent(ctx context.Context, ev audio.Event) {
	switch ev.Type {
	case audio.EventSinkAdded:
		d.refreshSettled(ctx)
		d.switcher.retry(ctx)

	case audio.EventSinkRemoved:
		d.refreshSettled(ctx)

	case audio.EventDefaultChanged:
		// Only the default moved, so read just its name if the sink is known.
		was := d.defaultID()
		if !d.refreshDefault(ctx) {
			d.refreshDevices(ctx)
		}
		if !d.isHolding() {
			d.switcher.settled(ctx, was)
		}
	}
}

// logSinkChanges runs on every list read, not on the pactl event. The event
// carries only the pulse index and often arrives after a read saw the change.
func (d *Daemon) logSinkChanges(before, after []audio.Device) {
	if added := missingFrom(after, before); len(added) > 0 {
		d.debugf("sink added: %s", strings.Join(added, ", "))
	}
	if removed := missingFrom(before, after); len(removed) > 0 {
		d.debugf("sink removed: %s", strings.Join(removed, ", "))
	}
}

// missingFrom returns the names of the devices in have that other lacks.
func missingFrom(have, other []audio.Device) []string {
	var names []string
	for _, dev := range have {
		if deviceByID(other, dev.ID) != nil {
			continue
		}
		name := dev.Name
		if name == "" {
			name = dev.ID
		}
		names = append(names, name)
	}
	return names
}

// rememberEarcups loads the earcup state the last daemon saved at path, so a
// headset that was off across a restart stays out of the fallback. A
// remembered "on" never switches to it. The file does not survive a reboot.
func (d *Daemon) rememberEarcups(path string) {
	on, at, ok := razer.LoadState(path)
	d.mu.Lock()
	d.earcupsFile = path
	if ok {
		d.earcupsKnown = true
		d.earcupsOn = on
	}
	d.mu.Unlock()
	if ok {
		state := "off"
		if on {
			state = "on"
		}
		d.infof("Barracuda earcups were %s at the last report, %s", state, at.Format("Jan 02 15:04"))
	}
}

// handleEarcups records a power report. Off falls back, on follows on_connect.
func (d *Daemon) handleEarcups(ctx context.Context, on bool) {
	before := d.defaultID()
	d.mu.Lock()
	d.earcupsKnown = true
	d.earcupsOn = on
	file := d.earcupsFile
	d.mu.Unlock()
	if file != "" {
		if err := razer.SaveState(file, on); err != nil {
			d.debugf("saving the earcup state: %v", err)
		}
	}

	d.refreshSettled(ctx)
	d.switcher.earcups(ctx, on, before)
}

// setDefault records why it switched, for the announcement. Callers must hold
// switchMu. SetDefault takes it for them.
func (d *Daemon) setDefault(ctx context.Context, deviceID string, reason switchReason, notify bool) error {
	d.mu.Lock()
	d.claim = &claim{id: deviceID, reason: reason, notify: notify}
	d.mu.Unlock()

	err := d.backend.SetDefaultSink(ctx, deviceID)
	if err == nil {
		d.refreshDevices(ctx)
	}

	// An unused claim means the set failed or changed nothing.
	d.mu.Lock()
	if d.claim != nil && d.claim.id == deviceID {
		d.claim = nil
	}
	d.mu.Unlock()
	return err
}

// SetDefault is the manual switch. It takes the same lock as the automatic
// switches. notify requests a desktop notification.
func (d *Daemon) SetDefault(ctx context.Context, deviceID string, notify bool) error {
	d.switchMu.Lock()
	defer d.switchMu.Unlock()
	return d.setDefault(ctx, deviceID, reasonManual, notify)
}

func (d *Daemon) refreshInitial(ctx context.Context) {
	d.refreshDevices(ctx)
	d.switcher.settled(ctx, "")
}

// refreshSettled rereads the sinks and lets the switcher fall back unless a
// hold is on. setDefault must not call it, since it already holds switchMu.
func (d *Daemon) refreshSettled(ctx context.Context) {
	was := d.defaultID()
	d.refreshDevices(ctx)
	if d.isHolding() {
		return
	}
	d.switcher.settled(ctx, was)
}

func (d *Daemon) refreshDevices(ctx context.Context) {
	devices, err := d.backend.ListSinks(ctx)
	if err != nil {
		d.errorf("refreshing devices failed: %v", err)
		return
	}
	d.storeDevices(devices)
}

// refreshDefault reports false when the default is not in the cached list.
func (d *Daemon) refreshDefault(ctx context.Context) bool {
	name, err := d.backend.DefaultSinkName(ctx)
	if err != nil {
		return false
	}
	devices := d.GetDevices()
	if deviceByID(devices, name) == nil {
		return false
	}
	for i := range devices {
		devices[i].IsDefault = devices[i].ID == name
	}
	d.storeDevices(devices)
	return true
}

// storeDevices wakes subscribers only on a real change. pactl reports many
// changes that alter nothing the UI draws.
func (d *Daemon) storeDevices(devices []audio.Device) {
	d.mu.Lock()
	// pactl still lists the Barracuda receiver with the earcups off, so the
	// power report is the only way to mark it unavailable.
	if d.earcupsKnown && !d.earcupsOn {
		for i := range devices {
			if razer.IsBarracuda(devices[i]) {
				devices[i].Available = false
			}
		}
	}
	before, listed := d.devices, d.listed
	changed := !slices.Equal(before, devices)
	d.devices = devices
	d.listed = true

	newDefault := ""
	for _, dev := range devices {
		if dev.IsDefault {
			newDefault = dev.ID
			break
		}
	}
	// The first reading is where things are, not a change to announce.
	moved := newDefault != "" && d.lastDefaultID != "" && newDefault != d.lastDefaultID
	var from departure
	if moved {
		from = departureOf(d.lastDefaultID, before, devices)
	}
	if newDefault != "" {
		d.lastDefaultID = newDefault
	}
	d.mu.Unlock()

	if listed {
		d.logSinkChanges(before, devices)
	}
	if moved {
		d.defaultMoved(newDefault, from)
	}
	if changed {
		d.changed()
	}
}

func (d *Daemon) setHolding(v bool) {
	d.mu.Lock()
	d.holding = v
	d.mu.Unlock()
}

func (d *Daemon) isHolding() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.holding
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

// ConfigPath needs no lock because it is fixed at startup.
func (d *Daemon) ConfigPath() string {
	return d.configPath
}

func (d *Daemon) Config() config.Config {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return copyConfig(d.cfg)
}

// Config reads outside a locked section go through these accessors.

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

// copyConfig copies the priority slice so callers never see UI edits.
func copyConfig(cfg config.Config) config.Config {
	out := cfg
	out.Priority = make([]config.PriorityEntry, len(cfg.Priority))
	copy(out.Priority, cfg.Priority)
	return out
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

// sleepCtx reports false if ctx ended before d elapsed.
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
