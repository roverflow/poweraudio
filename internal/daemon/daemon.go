// Package daemon is the long-running half of poweraudio. It watches BlueZ and
// the audio backend, hands switching decisions to the engine this machine
// supports, and serves the state a UI needs over a Unix socket. Everything a
// client can ask for goes through Handle or Subscribe; the rest of the package
// is unexported.
package daemon

import (
	"context"
	"os"
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

	// quietStart is how long after startup a failure to open the Barracuda
	// node is expected. The udev rule grants access when the login session
	// becomes active, which is a second or two after a boot-time start.
	quietStart = 30 * time.Second
)

// Holds keep the daemon from falling back while sinks are still arriving. At
// boot WirePlumber publishes cards one at a time over a second or more, and
// after a resume USB, HDMI and Bluetooth come back in any order. Falling back
// in the middle picked whatever happened to be there first, which on a desk
// with a monitor attached was the HDMI output, every boot. A hold ends once
// the sink list has been quiet for holdStable, or at its cap, and then the
// default is checked once. Sleep and shutdown hold with no cap at all, until
// logind says the machine is back.
//
// They are variables so tests can shorten them.
var (
	holdStable       = 1500 * time.Millisecond
	holdStartupMax   = 10 * time.Second
	holdResumeStable = 2 * time.Second
	holdResumeMax    = 15 * time.Second

	// resubscribeMin and resubscribeMax bound the wait before following
	// pactl subscribe again after it ended.
	resubscribeMin = time.Second
	resubscribeMax = 30 * time.Second
)

// configPollInterval is how often the config file's mtime is checked. It is a
// variable so tests do not have to wait two seconds for a reload.
var configPollInterval = 2 * time.Second

type Daemon struct {
	backend audio.Backend

	// configPath is where this daemon was told to read its config, so saves
	// from the UI land in the same file rather than always in the default one.
	configPath string
	startTime  time.Time

	mu            sync.RWMutex
	cfg           config.Config
	devices       []audio.Device
	lastDefaultID string
	events        []ipc.EventLog
	// claim is the switch this daemon is about to make. The sink list read
	// that shows it turns it into an announcement with the right reason.
	claim *claim
	// recentBT is when each Bluetooth device last connected, by MAC.
	recentBT map[string]time.Time
	// holding is true during a startup, sleep or resume hold.
	holding bool
	audio   probe.Report
	notes   notifier

	// earcupsKnown is false until the Barracuda dongle pushes a power report.
	// It does not repeat that report while the state holds, so a daemon that
	// starts with the earcups already off has nothing to act on yet.
	// earcupsOn is the last report. Byte 13 of report id 0x02.
	earcupsKnown bool
	earcupsOn    bool

	// switcher decides where the output goes. See switcher.go.
	switcher switcher
	// prober finds out what the sound stack can do. Tests replace it.
	prober func(context.Context) probe.Report

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
	d := &Daemon{
		cfg:        cfg,
		backend:    backend,
		configPath: configPath,
		startTime:  time.Now(),
		recentBT:   make(map[string]time.Time),
		subs:       make(map[*subscriber]struct{}),
		prober: func(ctx context.Context) probe.Report {
			return probe.Run(ctx, probe.Exec)
		},
	}
	d.switcher = newPactlSwitcher(d)
	return d
}

// sources are the event streams the loop follows. A nil channel is one that
// is not available on this machine, which select simply never picks.
type sources struct {
	bt    <-chan bluetooth.Event
	audio <-chan audio.Event
	link  <-chan razer.Event
	power <-chan power.State
}

// Run holds the event loop until ctx ends. IPC requests do not come through
// here: the server calls Handle and Subscribe directly, because serving them
// from this goroutine meant a held volume key queued behind a sink refresh.
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

	d.infof("daemon started with %s backend, %s switching", d.backend.Name(), d.switcher.name())
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

// reprobe asks what the sound stack can do and records the answer. It runs at
// startup and after the sound server comes back, which is when an upgrade
// takes effect.
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

// runEvents owns everything that touches the audio backend on a timer: the
// Bluetooth handlers, the debounced sink refresh, the retry that waits for a
// Bluetooth sink to appear, the holds, the reconnect to the sound server, and
// the config file watch.
func (d *Daemon) runEvents(ctx context.Context, src sources) {
	var (
		settle <-chan time.Time // a burst of sink changes is still arriving
		retry  <-chan time.Time // a Bluetooth sink has not turned up yet
		resub  <-chan time.Time // time to follow pactl subscribe again

		hold      <-chan time.Time // the sink list has been quiet long enough
		holdEnd   time.Time        // the hold's cap
		holdQuiet time.Duration    // how long the list must stay quiet

		// away is what logind last announced. Anything but Awake holds every
		// switch until logind says the machine is back.
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
	// extendHold restarts the quiet period after a sink came or went, but
	// never past the cap.
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
	// A stream handed in is followed as it is. Run hands in none, so the
	// daemon follows the backend's own, and follows it again if it ends.
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
				// pactl subscribe exits when the sound server restarts,
				// which is the standard fix for Bluetooth trouble. Not
				// following it again left a daemon that still answered
				// but no longer noticed an unplug.
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
				// The first event on a new stream is the proof that the
				// server is back.
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
			// The sink list is read again on the first event the new
			// stream carries. Reading it here would log a failure on
			// every attempt while the server is still down.
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

// endHold lets switching resume and checks the default once, now that the
// sink list has stopped moving. before is the default from before a sleep or
// a cancelled shutdown, and empty after a startup hold: nobody needs to be told
// where audio is playing at login, but after a resume a different output is
// worth a notification.
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
		// Before login the node is root-only, and the watcher retries
		// every two seconds until the session's access is granted.
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
	state := "disconnected"
	if ev.Connected {
		state = "connected"
	}
	// A mouse or keyboard reconnecting after idle used to park a 15 second
	// wait for an audio sink that would never come, and take the place of a
	// headset that was waiting for its own.
	if ev.NotAudio {
		d.debugf("bluetooth %s: %s (%s), not an audio device", state, ev.DeviceName, ev.MACAddress)
		return
	}
	d.infof("bluetooth %s: %s (%s)", state, ev.DeviceName, ev.MACAddress)

	if ev.Connected {
		// Read before anything reacts to the connect, so it names the
		// device the person was listening on.
		before := d.defaultID()
		d.noteConnect(ev.MACAddress)

		// BlueZ reports the link before PipeWire publishes the sink, so give
		// it a head start before going to look.
		if !sleepCtx(ctx, time.Duration(d.switching().SwitchDelayMs)*time.Millisecond) {
			return
		}
		d.refreshSettled(ctx)
		d.switcher.connected(ctx, ev.MACAddress, ev.DeviceName, before)
		return
	}

	d.switcher.disconnected(ctx, ev.MACAddress)
	if !sleepCtx(ctx, disconnectSettle) {
		return
	}
	// The wait gives the sink time to disappear. refreshSettled then moves
	// the output only when the sink that was playing has gone or can no
	// longer play, so an idle second headset disconnecting changes nothing.
	d.refreshSettled(ctx)
}

func (d *Daemon) handleAudioEvent(ctx context.Context, ev audio.Event) {
	switch ev.Type {
	case audio.EventSinkAdded:
		before := d.GetDevices()
		d.refreshSettled(ctx)
		d.logSinkDiff("sink added", d.GetDevices(), before, ev.DeviceID)
		d.switcher.retry(ctx)

	case audio.EventSinkRemoved:
		before := d.GetDevices()
		d.refreshSettled(ctx)
		d.logSinkDiff("sink removed", before, d.GetDevices(), ev.DeviceID)

	case audio.EventDefaultChanged:
		// Only the default moved, so one call to read it beats listing
		// every sink. The full list is read when the new default is a sink
		// the daemon has not seen yet.
		was := d.defaultID()
		if !d.refreshDefault(ctx) {
			d.refreshDevices(ctx)
		}
		if !d.isHolding() {
			d.switcher.settled(ctx, was)
		}
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

// handleEarcups records a Barracuda power report and moves the output.
// Off runs the same fallback as a sink disappearing. On follows on_connect,
// the same rule as a Bluetooth headset connecting.
func (d *Daemon) handleEarcups(ctx context.Context, on bool) {
	before := d.defaultID()
	d.mu.Lock()
	d.earcupsKnown = true
	d.earcupsOn = on
	d.mu.Unlock()

	d.refreshSettled(ctx)
	d.switcher.earcups(ctx, on, before)
}

// setDefault switches the output and records why, so the change the next sink
// list read reveals is announced with that reason. Callers already holding
// switchMu use this; SetDefault takes the lock for them.
func (d *Daemon) setDefault(ctx context.Context, deviceID string, reason switchReason, notify bool) error {
	d.mu.Lock()
	d.claim = &claim{id: deviceID, reason: reason, notify: notify}
	d.mu.Unlock()

	err := d.backend.SetDefaultSink(ctx, deviceID)
	if err == nil {
		d.refreshDevices(ctx)
	}

	// A claim the read did not use is stale: the set failed, or the device
	// was already the default and nothing changed.
	d.mu.Lock()
	if d.claim != nil && d.claim.id == deviceID {
		d.claim = nil
	}
	d.mu.Unlock()
	return err
}

// SetDefault is the manual switch behind the UI and the CLI. It waits on the
// same lock the automatic switches use, so a keypress and a Bluetooth connect
// landing at the same moment cannot leave the output somewhere neither of
// them picked. notify asks for a desktop notification, which a hotkey wants
// and the terminal UI does not.
func (d *Daemon) SetDefault(ctx context.Context, deviceID string, notify bool) error {
	d.switchMu.Lock()
	defer d.switchMu.Unlock()
	return d.setDefault(ctx, deviceID, reasonManual, notify)
}

// refreshInitial loads the sink list and moves off a default that cannot
// play, comparing against nothing earlier. It is what the end of a hold does.
func (d *Daemon) refreshInitial(ctx context.Context) {
	d.refreshDevices(ctx)
	d.switcher.settled(ctx, "")
}

// refreshSettled re-reads the sink list and lets the switcher move the output
// off whatever was default when that sink is gone or can no longer play.
// During a hold it only reads. setDefault calls refreshDevices instead: it
// already holds switchMu, and falling back from inside it would lock that
// mutex twice.
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

// refreshDefault reads only the default sink's name and updates the cached
// list to match. It reports false when that name is not in the list, which
// means a full read is needed.
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

// storeDevices installs a fresh sink list. Subscribers are only woken when
// something in it changed: pactl reports a change on every sink for reasons
// that alter nothing the UI draws, and each wake built and sent a snapshot.
func (d *Daemon) storeDevices(devices []audio.Device) {
	d.mu.Lock()
	// pactl still lists the Barracuda receiver when the earcups are off.
	// The link report is the only signal, so a known-off headset is taken
	// out of the fallback here.
	if d.earcupsKnown && !d.earcupsOn {
		for i := range devices {
			if razer.IsBarracuda(devices[i]) {
				devices[i].Available = false
			}
		}
	}
	changed := !slices.Equal(d.devices, devices)
	d.devices = devices

	newDefault := ""
	for _, dev := range devices {
		if dev.IsDefault {
			newDefault = dev.ID
			break
		}
	}
	// The first reading is where things are, not a change to announce.
	moved := newDefault != "" && d.lastDefaultID != "" && newDefault != d.lastDefaultID
	if newDefault != "" {
		d.lastDefaultID = newDefault
	}
	d.mu.Unlock()

	if moved {
		d.defaultMoved(newDefault)
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
