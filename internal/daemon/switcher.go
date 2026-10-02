package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/priority"
	"github.com/roverflow/poweraudio/internal/razer"
)

// switcher is where the daemon hands off the decision of where the output
// goes. The daemon owns everything a person sees: the sink list, the log, the
// notifications, the IPC socket. It tells the switcher what happened and the
// switcher decides what to do about it.
//
// Today there is one engine, pactlSwitcher, which makes every decision itself
// and calls `pactl set-default-sink`. It is what runs on PulseAudio, on
// WirePlumber 0.4 and on any machine the probe does not recognise. On
// WirePlumber 0.5 a second engine will hand the decisions to a WirePlumber
// hook instead, and the daemon will not need to change to use it.
//
// The daemon calls these from its event goroutine, except where noted, and
// never while holding d.mu.
type switcher interface {
	// name is what the status screen calls this engine.
	name() string

	// connected is a Bluetooth audio device arriving. before is the default
	// output at the moment BlueZ reported it, ahead of anything the session
	// manager did in response, so it is the device the person was listening
	// on.
	connected(ctx context.Context, mac, name, before string)

	// disconnected is a Bluetooth audio device going away.
	disconnected(ctx context.Context, mac string)

	// earcups is the Barracuda headset powering on or off. before is the
	// default output when the report arrived.
	earcups(ctx context.Context, on bool, before string)

	// settled runs after the sink list was read again. was is the default
	// before the read, or empty when there is no earlier reading to compare
	// with, such as the end of a startup or resume hold.
	settled(ctx context.Context, was string)

	// retry is the safety-net timer, fired while waiting reports true.
	retry(ctx context.Context)

	// waiting reports whether a connected device's sink has not turned up
	// yet, so the event loop knows to keep the retry timer running.
	waiting() bool
}

// pendingBT is a Bluetooth device that has connected but whose audio sink
// the server has not published yet.
type pendingBT struct {
	mac    string
	name   string
	before string
	expiry time.Time
}

// pactlSwitcher decides everything itself and drives the server through the
// audio backend. It reads the daemon's cached sink list, so callers refresh
// before calling in.
type pactlSwitcher struct {
	d *Daemon

	mu sync.Mutex
	// pending holds every connected device still waiting for its sink,
	// keyed by upper-case MAC. One slot used to mean a Bluetooth mouse
	// reconnecting while a headset waited took the headset's place, and the
	// headset never got the output.
	pending map[string]*pendingBT
	// previousID is where the output was before the daemon last moved it
	// onto a device, for on_disconnect = "previous".
	previousID string
	// stranded is true once a fallback has found nothing that can play. It
	// is said once per outage: every sink leaving at logout used to log the
	// same warning again, nine times in 70ms.
	stranded bool
}

func newPactlSwitcher(d *Daemon) *pactlSwitcher {
	return &pactlSwitcher{d: d, pending: make(map[string]*pendingBT)}
}

func (s *pactlSwitcher) name() string { return "pactl" }

func (s *pactlSwitcher) connected(ctx context.Context, mac, name, before string) {
	if s.d.switching().OnConnect == "never" {
		return
	}
	if s.trySwitchToBT(ctx, mac, name, before) {
		return
	}

	s.d.infof("bluetooth device connected but has no audio sink yet, waiting")
	s.mu.Lock()
	s.pending[strings.ToUpper(mac)] = &pendingBT{
		mac:    mac,
		name:   name,
		before: before,
		expiry: time.Now().Add(pendingTTL),
	}
	s.mu.Unlock()
}

func (s *pactlSwitcher) disconnected(_ context.Context, mac string) {
	// Only the device being waited on cancels its own wait. Another headset
	// going away says nothing about this one.
	s.mu.Lock()
	delete(s.pending, strings.ToUpper(mac))
	s.mu.Unlock()
}

// earcups only acts on power-on. Power-off needs nothing here: the daemon
// marks the sink unable to play, and settled moves the output off it.
func (s *pactlSwitcher) earcups(ctx context.Context, on bool, before string) {
	if !on || s.d.switching().OnConnect == "never" {
		return
	}
	if dev := findBarracuda(s.d.GetDevices()); dev != nil {
		s.trySwitchTo(ctx, *dev, before, reasonEarcups)
	}
}

func (s *pactlSwitcher) settled(ctx context.Context, was string) {
	devices := s.d.GetDevices()
	// The sink that was playing has gone, or can no longer play.
	if was != "" {
		if dev := deviceByID(devices, was); dev == nil || !dev.Usable() {
			s.fallback(ctx)
			return
		}
	}
	// The default is somewhere that cannot play: the placeholder the server
	// picks when nothing else is left, a port with nothing plugged in, a
	// Barracuda whose earcups are off, or a sink that has gone while the
	// server has not named a new default yet. The Barracuda case is what the
	// session manager restores from its own history after a resume.
	cur := s.d.defaultID()
	if cur == "" {
		return
	}
	if dev := deviceByID(devices, cur); dev == nil || !dev.Usable() {
		s.fallback(ctx)
		return
	}
	// Something can play again, so the next time nothing can is news.
	s.setStranded(false)
}

func (s *pactlSwitcher) retry(ctx context.Context) {
	s.mu.Lock()
	waiting := make([]*pendingBT, 0, len(s.pending))
	for _, p := range s.pending {
		waiting = append(waiting, p)
	}
	s.mu.Unlock()

	never := s.d.switching().OnConnect == "never"
	for _, p := range waiting {
		switch {
		case time.Now().After(p.expiry):
			s.clearPending(p)
			s.d.warnf("giving up waiting for the audio sink of %s", p.name)
		case never:
			s.clearPending(p)
		case s.trySwitchToBT(ctx, p.mac, p.name, p.before):
			s.clearPending(p)
		}
	}
}

func (s *pactlSwitcher) waiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}

// clearPending drops p only if it is still the attempt in flight for that
// device, so a newer connection is not thrown away by an older one finishing.
func (s *pactlSwitcher) clearPending(p *pendingBT) {
	s.mu.Lock()
	key := strings.ToUpper(p.mac)
	if s.pending[key] == p {
		delete(s.pending, key)
	}
	s.mu.Unlock()
}

// trySwitchToBT finds the sink belonging to a connected Bluetooth device and
// makes it the default, honouring on_connect. It reports whether the sink
// existed, so callers know whether there is any point waiting longer.
func (s *pactlSwitcher) trySwitchToBT(ctx context.Context, mac, name, before string) bool {
	dev := findBTDevice(s.d.GetDevices(), mac, name)
	if dev == nil {
		return false
	}
	return s.trySwitchTo(ctx, *dev, before, reasonConnect)
}

// trySwitchTo makes dev the default, honouring on_connect. A sink that cannot
// play is refused, so a power-off report cannot be turned around into a
// switch back onto it. A switch declined on priority grounds still reports
// true: the sink is there, the answer is just no.
func (s *pactlSwitcher) trySwitchTo(ctx context.Context, dev audio.Device, before string, reason switchReason) bool {
	d := s.d
	d.switchMu.Lock()
	defer d.switchMu.Unlock()

	if !dev.Usable() {
		return false
	}
	current := d.defaultID()

	// The session manager often gets there first: WirePlumber remembers the
	// headset and restores it the moment its sink appears. Switching again
	// would only repeat the announcement, and comparing the device's rank
	// against itself logged "X is not ranked above X".
	if dev.ID == current {
		s.rememberPrevious(before, dev.ID)
		d.debugf("%s is already the default", dev.Name)
		return true
	}

	if d.switching().OnConnect == "priority" {
		// A current default that cannot play is no reason to stay put.
		if cur := deviceByID(d.GetDevices(), current); cur != nil && cur.Usable() {
			entries := d.priorities()
			newRank := priority.Rank(dev, entries)
			currentRank := priority.Rank(*cur, entries)
			if newRank >= currentRank {
				d.infof("%s", skipReason(dev, *cur, newRank, currentRank, len(entries)))
				return true
			}
		}
	}

	if before == "" {
		before = current
	}
	s.rememberPrevious(before, dev.ID)
	if err := d.setDefault(ctx, dev.ID, reason, false); err != nil {
		d.errorf("switch failed: %v", err)
		return true
	}
	d.infof("switched to %s", dev.Name)
	return true
}

// rememberPrevious records where the output was before it moved onto target.
// The placeholder and target itself are never worth going back to, so they
// leave the last good answer in place.
func (s *pactlSwitcher) rememberPrevious(id, target string) {
	if id == "" || id == target || id == audio.PlaceholderID {
		return
	}
	s.mu.Lock()
	s.previousID = id
	s.mu.Unlock()
}

// fallback picks where the output goes once the device you were listening on
// has gone away or can no longer play. It does nothing when the output is
// already there, so a second report of the same departure is quiet.
func (s *pactlSwitcher) fallback(ctx context.Context) {
	d := s.d
	d.switchMu.Lock()
	defer d.switchMu.Unlock()

	devices := d.GetDevices()

	var target *audio.Device
	if d.switching().OnDisconnect == "previous" {
		s.mu.Lock()
		previousID := s.previousID
		s.mu.Unlock()
		if dev := deviceByID(devices, previousID); dev != nil && dev.Usable() {
			target = dev
		}
	}
	// Either the ranking was asked for, or the device that was playing before
	// is gone too. Leaving the output wherever the session happened to put it
	// is the behaviour this daemon exists to avoid.
	if target == nil {
		target = priority.Best(devices, d.priorities())
	}
	if target == nil {
		if s.setStranded(true) {
			d.infof("no output left to fall back to")
		}
		return
	}
	s.setStranded(false)
	if target.ID == d.defaultID() {
		return
	}

	if err := d.setDefault(ctx, target.ID, reasonFallback, false); err != nil {
		d.errorf("fallback switch failed: %v", err)
		return
	}
	d.infof("fallback to %s", target.Name)
}

// setStranded records whether the last fallback found nothing to play
// through, and reports whether that is a change.
func (s *pactlSwitcher) setStranded(v bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.stranded != v
	s.stranded = v
	return changed
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

func findBarracuda(devices []audio.Device) *audio.Device {
	for i := range devices {
		if razer.IsBarracuda(devices[i]) {
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
