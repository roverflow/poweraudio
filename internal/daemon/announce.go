package daemon

import (
	"strings"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/notify"
)

// switchReason is why the default output moved. Every change is announced
// from one place, defaultMoved, which runs whenever a sink list read shows a
// new default, whoever made the change. The reason decides the wording and
// whether a notification is worth showing at all.
type switchReason int

const (
	// reasonExternal is a change poweraudio did not make: the desktop's
	// sound settings, pavucontrol, or the session manager on its own.
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

// recentBTWindow is how long after a Bluetooth connect or disconnect a change
// of default still counts as caused by it. The session manager switching to a
// headset it remembers, or away from one that left, lands within a second or
// two of the link changing.
const recentBTWindow = 10 * time.Second

// claim is a switch this daemon is about to make, so the change it causes is
// announced with the right reason rather than as someone else's doing.
type claim struct {
	id     string
	reason switchReason
	notify bool
}

// notifier is the slice of notify.Notifier the daemon uses, so tests can
// record notices instead of sending them.
type notifier interface {
	Show(notify.Notice)
}

// takeClaim returns the reason recorded for a switch to id and clears it.
// A claim for some other sink is left alone: the change it expects may still
// be on its way.
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

// noteConnect records a Bluetooth device connecting, so a switch onto it that
// the session manager makes is announced as the connect it was.
func (d *Daemon) noteConnect(mac string) { d.noteBT(d.recentBT, mac) }

// noteDisconnect records a Bluetooth device going away, so a switch off it
// that the session manager makes is announced as the fallback it was.
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

// departure is the default a change moved away from.
type departure struct {
	// dev is that device as the read before the change saw it, and nil when
	// that read did not list it.
	dev *audio.Device
	// gone is true when the read that shows the change no longer lists the
	// device, or lists it as unable to play.
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

// wentAway reports whether the device the default moved off had left. The
// sink may already be missing from the list, or still listed for a moment
// after its Bluetooth link dropped.
func (d *Daemon) wentAway(from departure) bool {
	return from.dev != nil && (from.gone || d.disconnectedRecently(from.dev))
}

// defaultMoved runs once for every change of default that a sink list read
// reveals. from is the default it moved off. It must not be called while
// holding d.mu.
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
			// WirePlumber picks a new default within a second of a headset
			// disconnecting, before this daemon's own fallback runs. That is
			// the headset leaving, not someone changing the output, and it
			// used to be announced as "Changed outside poweraudio".
			c.reason = reasonFallback
			msg += " after " + from.dev.Name + " went away"
		}
		// During a hold the default moves several times as sinks come back,
		// and only where it ends up is worth an info line.
		if d.isHolding() {
			d.debugf("%s", msg)
		} else {
			d.infof("%s", msg)
		}
	}
	d.announce(dev, c)
}

// announce decides whether a change deserves a desktop notification and
// shows it. Nothing is shown during a startup or resume hold, for the
// placeholder, or for a change the person made with poweraudio unless they
// asked.
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

// iconFor picks a freedesktop icon name that matches the kind of output, where
// every notification used to show headphones, HDMI included.
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
