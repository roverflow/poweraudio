package daemon

import (
	"strings"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/notify"
)

// switchReason is why the default moved. defaultMoved announces every change
// from one place, and the reason picks the wording and whether to notify.
type switchReason int

const (
	// reasonExternal is a change poweraudio did not make.
	reasonExternal switchReason = iota
	reasonConnect
	reasonEarcups
	reasonFallback
	reasonManual
	reasonResume
)

func (r switchReason) body() string {
	switch r {
	case reasonConnect:
		return "Connected"
	case reasonEarcups:
		return "Headset powered on"
	case reasonFallback:
		return "The previous output went away"
	case reasonManual:
		return "Selected with poweraudio"
	case reasonResume:
		return "After waking from sleep"
	default:
		return "Changed outside poweraudio"
	}
}

// recentBTWindow is how long after a Bluetooth link change a default change
// counts as caused by it. WirePlumber switches within a second or two.
const recentBTWindow = 10 * time.Second

type claim struct {
	id     string
	reason switchReason
	notify bool
}

type notifier interface {
	Show(notify.Notice)
}

// takeClaim returns and clears the claim for id. A claim for another sink
// stays, because its change may still be on the way.
func (d *Daemon) takeClaim(id string) (claim, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.claim == nil || d.claim.id != id {
		return claim{}, false
	}
	c := *d.claim
	d.claim = nil
	return c, true
}

// noteConnect makes a session manager switch onto mac count as a connect.
func (d *Daemon) noteConnect(mac string) { d.noteBT(d.recentBT, mac) }

// noteDisconnect makes a session manager switch off mac count as a fallback.
func (d *Daemon) noteDisconnect(mac string) { d.noteBT(d.goneBT, mac) }

func (d *Daemon) noteBT(seen map[string]time.Time, mac string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for k, at := range seen {
		if now.Sub(at) > recentBTWindow {
			delete(seen, k)
		}
	}
	seen[strings.ToUpper(mac)] = now
}

func (d *Daemon) connectedRecently(dev *audio.Device) bool {
	return d.seenRecently(d.recentBT, dev)
}

func (d *Daemon) disconnectedRecently(dev *audio.Device) bool {
	return d.seenRecently(d.goneBT, dev)
}

func (d *Daemon) seenRecently(seen map[string]time.Time, dev *audio.Device) bool {
	if dev == nil || dev.MACAddress == "" {
		return false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	at, ok := seen[strings.ToUpper(dev.MACAddress)]
	return ok && time.Since(at) <= recentBTWindow
}

type departure struct {
	dev  *audio.Device
	gone bool
}

func departureOf(id string, before, after []audio.Device) departure {
	var out departure
	if dev := deviceByID(before, id); dev != nil {
		c := *dev
		out.dev = &c
	}
	now := deviceByID(after, id)
	out.gone = now == nil || !now.Usable()
	return out
}

// wentAway also catches a sink still listed just after its link dropped.
func (d *Daemon) wentAway(from departure) bool {
	return from.dev != nil && (from.gone || d.disconnectedRecently(from.dev))
}

// defaultMoved must not be called while holding d.mu.
func (d *Daemon) defaultMoved(id string, from departure) {
	dev := deviceByID(d.GetDevices(), id)

	c, ours := d.takeClaim(id)
	if !ours {
		c = claim{id: id, reason: reasonExternal}
		name := id
		if dev != nil {
			name = dev.Name
		}
		msg := "default device changed to " + name
		switch {
		case d.connectedRecently(dev):
			c.reason = reasonConnect
		case d.wentAway(from):
			// WirePlumber picks a new default within a second of a
			// headset leaving, before the daemon's fallback runs. That is
			// a fallback, not an external change.
			c.reason = reasonFallback
			msg += " after " + from.dev.Name + " went away"
		}
		// During a hold the default moves several times, so log it at debug.
		if d.isHolding() {
			d.debugf("%s", msg)
		} else {
			d.infof("%s", msg)
		}
	}
	d.announce(dev, c)
}

// announce skips holds, the placeholder, and manual switches that did not
// ask for a notification.
func (d *Daemon) announce(dev *audio.Device, c claim) {
	if dev == nil || dev.IsPlaceholder() || d.isHolding() {
		return
	}
	cfg := d.notifications()
	if !cfg.Enabled {
		return
	}
	switch c.reason {
	case reasonManual:
		if !c.notify {
			return
		}
	case reasonExternal:
		if !cfg.OnDeviceChange {
			return
		}
	}

	d.mu.RLock()
	notes := d.notes
	d.mu.RUnlock()
	if notes == nil {
		return
	}
	notes.Show(notify.Notice{
		Summary: "Playing on " + dev.Name,
		Body:    c.reason.body(),
		Icon:    iconFor(*dev),
	})
}

func iconFor(dev audio.Device) string {
	switch dev.Type {
	case audio.DeviceTypeBluetooth, audio.DeviceTypeHeadphone:
		return "audio-headphones"
	case audio.DeviceTypeHDMI:
		return "video-display"
	case audio.DeviceTypeSpeaker:
		return "audio-speakers"
	default:
		return "audio-card"
	}
}
