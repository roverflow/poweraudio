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

// recentConnectWindow is how long after a Bluetooth connect a change onto
// that device still counts as caused by it. The session manager switching to
// a headset it remembers lands within a second or two of the link.
const recentConnectWindow = 10 * time.Second

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
func (d *Daemon) noteConnect(mac string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for k, at := range d.recentBT {
		if now.Sub(at) > recentConnectWindow {
			delete(d.recentBT, k)
		}
	}
	d.recentBT[strings.ToUpper(mac)] = now
}

func (d *Daemon) connectedRecently(dev *audio.Device) bool {
	if dev == nil || dev.MACAddress == "" {
		return false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	at, ok := d.recentBT[strings.ToUpper(dev.MACAddress)]
	return ok && time.Since(at) <= recentConnectWindow
}

// defaultMoved runs once for every change of default that a sink list read
// reveals. It must not be called while holding d.mu.
func (d *Daemon) defaultMoved(id string) {
	dev := deviceByID(d.GetDevices(), id)

	c, ours := d.takeClaim(id)
	if !ours {
		c = claim{id: id, reason: reasonExternal}
		if d.connectedRecently(dev) {
			c.reason = reasonConnect
		}
		name := id
		if dev != nil {
			name = dev.Name
		}
		// During a hold the default moves several times as sinks come back,
		// and only where it ends up is worth an info line.
		if d.isHolding() {
			d.debugf("default device changed to %s", name)
		} else {
			d.infof("default device changed to %s", name)
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
