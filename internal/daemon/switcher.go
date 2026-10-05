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

// switcher decides where the output goes. The daemon tells it what happened.
// pactlSwitcher, which calls `pactl set-default-sink`, is the only engine.
//
// The daemon calls these from its event goroutine and never while holding
// d.mu, except name. Snapshot calls name under d.mu, so it must not lock.
type switcher interface {
	name() string

	// before is the default when BlueZ reported the connect, ahead of any
	// session manager switch.
	connected(ctx context.Context, mac, name, before string)

	disconnected(ctx context.Context, mac string)

	// before is the default when the report arrived.
	earcups(ctx context.Context, on bool, before string)

	// settled runs after a sink list read. was is the default before it, or
	// empty when there is nothing to compare, as at the end of a hold.
	settled(ctx context.Context, was string)

	retry(ctx context.Context)

	waiting() bool
}

type pendingBT struct {
	mac    string
	name   string
	before string
	expiry time.Time
}

// pactlSwitcher reads the cached sink list, so callers refresh first.
type pactlSwitcher struct {
	d *Daemon

	mu sync.Mutex
	// pending is keyed by upper-case MAC so waits for several devices coexist.
	pending    map[string]*pendingBT
	previousID string
	// stranded makes the daemon log "no output left" once per outage.
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
	// Only the device being waited on cancels its own wait.
	s.mu.Lock()
	delete(s.pending, strings.ToUpper(mac))
	s.mu.Unlock()
}

// earcups acts only on power-on. On power-off, settled moves the output off
// the sink the daemon marked unavailable.
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
	// The default cannot play. It may be the placeholder, an unplugged port,
	// a Barracuda with its earcups off, or a sink that left before the server
	// named a new default. WirePlumber restores the Barracuda after a resume.
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

// clearPending drops p only if it is still current, so an older attempt
// finishing cannot drop a newer connection.
func (s *pactlSwitcher) clearPending(p *pendingBT) {
	s.mu.Lock()
	key := strings.ToUpper(p.mac)
	if s.pending[key] == p {
		delete(s.pending, key)
	}
	s.mu.Unlock()
}

// trySwitchToBT reports false while the sink is missing, so callers wait.
func (s *pactlSwitcher) trySwitchToBT(ctx context.Context, mac, name, before string) bool {
	dev := findBTDevice(s.d.GetDevices(), mac, name)
	if dev == nil {
		return false
	}
	return s.trySwitchTo(ctx, *dev, before, reasonConnect)
}

// trySwitchTo refuses a sink that cannot play. It reports true whenever the
// sink is usable, even when priority declines the switch.
func (s *pactlSwitcher) trySwitchTo(ctx context.Context, dev audio.Device, before string, reason switchReason) bool {
	d := s.d
	d.switchMu.Lock()
	defer d.switchMu.Unlock()

	if !dev.Usable() {
		return false
	}
	current := d.defaultID()

	// WirePlumber often restores the headset as its sink appears. Switching
	// again would repeat the announcement and log "X is not ranked above X".
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

// rememberPrevious skips the placeholder and target, keeping the last answer.
func (s *pactlSwitcher) rememberPrevious(id, target string) {
	if id == "" || id == target || id == audio.PlaceholderID {
		return
	}
	s.mu.Lock()
	s.previousID = id
	s.mu.Unlock()
}

// fallback does nothing when the output is already there, so repeats stay
// quiet.
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
	// Never leave the output wherever the session happened to put it.
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

// setStranded reports whether the stranded state changed.
func (s *pactlSwitcher) setStranded(v bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.stranded != v
	s.stranded = v
	return changed
}

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

// findBTDevice matches by MAC first, since two headsets can share a model
// name, then by alias for backends that report no MAC.
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
