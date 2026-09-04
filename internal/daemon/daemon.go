package daemon

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/bluetooth"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
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

	// maxEvents is the size of the in-memory log the status screen reads.
	maxEvents = 200
)

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

	ipcRequests chan IPCRequest
}

type IPCRequest struct {
	Request  Request
	Response chan Response
}

func New(cfg config.Config, backend audio.Backend, configPath string) *Daemon {
	return &Daemon{
		cfg:         cfg,
		backend:     backend,
		configPath:  configPath,
		startTime:   time.Now(),
		ipcRequests: make(chan IPCRequest, 16),
	}
}

func (d *Daemon) IPCChannel() chan<- IPCRequest {
	return d.ipcRequests
}

func (d *Daemon) Run(ctx context.Context) error {
	d.logEvent("daemon started with %s backend", d.backend.Name())

	d.refreshDevices(ctx)

	var btEvents <-chan bluetooth.Event
	btMon, err := bluetooth.NewMonitor()
	if err != nil {
		d.logEvent("bluetooth monitoring unavailable: %v", err)
	} else {
		d.btMonitor = btMon
		defer btMon.Close()
		ch, err := btMon.Subscribe(ctx)
		if err != nil {
			d.logEvent("bluetooth subscribe failed: %v", err)
		} else {
			btEvents = ch
			d.logEvent("bluetooth monitoring active")
		}
	}

	audioEvents, err := d.backend.SubscribeEvents(ctx)
	if err != nil {
		d.logEvent("audio event subscription failed: %v", err)
	}

	// Device events run on their own goroutine. Handling one means sleeping
	// for the switch delay and then shelling out several times, and doing that
	// here left the UI hanging on every Bluetooth connect.
	go d.runEvents(ctx, btEvents, audioEvents)

	for {
		select {
		case <-ctx.Done():
			d.logEvent("daemon stopping")
			return ctx.Err()

		case req, ok := <-d.ipcRequests:
			if !ok {
				return nil
			}
			d.handleIPC(ctx, req)
		}
	}
}

// runEvents owns everything that touches the audio backend on a timer: the
// Bluetooth handlers, the debounced sink refresh, and the retry that waits for
// a Bluetooth sink to appear.
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
		}
	}
}

func (d *Daemon) handleBluetoothEvent(ctx context.Context, ev bluetooth.Event) {
	if ev.Connected {
		d.logEvent("bluetooth connected: %s (%s)", ev.DeviceName, ev.MACAddress)

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

		d.logEvent("bluetooth device connected but has no audio sink yet, waiting")
		d.mu.Lock()
		d.pending = &pendingBT{
			mac:    ev.MACAddress,
			name:   ev.DeviceName,
			expiry: time.Now().Add(pendingTTL),
		}
		d.mu.Unlock()
		return
	}

	d.logEvent("bluetooth disconnected: %s (%s)", ev.DeviceName, ev.MACAddress)

	d.mu.Lock()
	d.pending = nil
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
		d.logEvent("sink added: %s", ev.DeviceID)
		d.refreshDevices(ctx)
		d.attemptPending(ctx)

	case audio.EventSinkRemoved:
		d.logEvent("sink removed: %s", ev.DeviceID)
		d.refreshDevices(ctx)

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
		d.logEvent("default device changed to %s", name)
		d.notify("Default Device Changed", fmt.Sprintf("Now playing through %s", name))
	}
}

// trySwitchToBT finds the sink belonging to a connected Bluetooth device and
// makes it the default, honouring on_connect. It reports whether the sink
// existed, so callers know whether there is any point waiting longer. A switch
// declined on priority grounds still counts: the sink is there, the answer is
// just no.
func (d *Daemon) trySwitchToBT(ctx context.Context, mac, name string) bool {
	d.switchMu.Lock()
	defer d.switchMu.Unlock()

	dev := findBTDevice(d.GetDevices(), mac, name)
	if dev == nil {
		return false
	}

	if d.switching().OnConnect == "priority" {
		priorities := d.priorities()
		if current, err := d.backend.GetDefaultSink(ctx); err == nil && current != nil {
			if DevicePriority(*dev, priorities) >= DevicePriority(*current, priorities) {
				d.logEvent("skipping switch: %s has lower priority", dev.Name)
				return true
			}
		}
	}

	d.savePrevious(ctx)
	if err := d.setDefault(ctx, dev.ID); err != nil {
		d.logEvent("switch failed: %v", err)
		return true
	}
	d.logEvent("switched to %s", dev.Name)
	d.notify("Audio Switched", fmt.Sprintf("Now playing through %s", dev.Name))
	return true
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
		target = FindBestDevice(devices, d.priorities())
	}
	if target == nil {
		d.logEvent("no fallback device available")
		return
	}

	if err := d.setDefault(ctx, target.ID); err != nil {
		d.logEvent("fallback switch failed: %v", err)
		return
	}
	d.logEvent("fallback to %s", target.Name)
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
		d.logEvent("giving up waiting for the audio sink of %s", p.name)
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
		log.Printf("refresh devices: %v", err)
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
}

func (d *Daemon) savePrevious(ctx context.Context) {
	current, err := d.backend.GetDefaultSink(ctx)
	if err != nil {
		return
	}
	d.mu.Lock()
	d.previousID = current.ID
	d.mu.Unlock()
}

func (d *Daemon) logEvent(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Println(msg)
	d.mu.Lock()
	d.events = append(d.events, ipc.EventLog{Time: time.Now(), Message: msg})
	if len(d.events) > maxEvents {
		d.events = d.events[len(d.events)-maxEvents:]
	}
	d.mu.Unlock()
}

func (d *Daemon) GetDevices() []audio.Device {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]audio.Device, len(d.devices))
	copy(out, d.devices)
	return out
}

func (d *Daemon) GetEvents(limit int) []ipc.EventLog {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if limit <= 0 || limit > len(d.events) {
		limit = len(d.events)
	}
	out := make([]ipc.EventLog, limit)
	copy(out, d.events[len(d.events)-limit:])
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
	return d.cfg
}

func (d *Daemon) UpdatePriorities(priorities []config.PriorityEntry) {
	d.mu.Lock()
	d.cfg.Priority = priorities
	d.mu.Unlock()
}

// The event goroutines read config while IPC requests write it, so every read
// outside handleIPC goes through one of these.

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
