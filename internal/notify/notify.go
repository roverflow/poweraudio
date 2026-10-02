// Package notify shows desktop notifications over the session bus.
//
// It talks to org.freedesktop.Notifications directly instead of starting
// notify-send for every message. That keeps the id of the last notification,
// so a new one replaces it on screen instead of stacking, and it lets a quick
// burst of changes collapse into the one that matters: a fallback, the session
// manager's own correction a moment later, and a second fallback show up as a
// single notification naming where the output ended up.
package notify

import (
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// Notice is one notification.
type Notice struct {
	Summary string
	Body    string
	Icon    string
}

const (
	// settleDelay is how long a notice waits for a newer one to replace it.
	// The switches it collapses arrive within a few tens of milliseconds.
	settleDelay = 300 * time.Millisecond

	// expireMs is how long the notification stays up, in milliseconds.
	expireMs = 4000

	busName = "org.freedesktop.Notifications"
	path    = "/org/freedesktop/Notifications"
)

// Notifier delivers notices. Show never blocks, so it is safe to call from the
// daemon's event loop.
type Notifier struct {
	delay   time.Duration
	deliver func(Notice)

	mu      sync.Mutex
	pending *Notice
	timer   *time.Timer
	closed  bool
}

// New returns a Notifier that sends to the desktop's notification server.
// logf receives a line when a notice could not be shown, which is normal while
// the desktop is still starting.
func New(logf func(format string, args ...any)) *Notifier {
	b := &bus{logf: logf}
	return newNotifier(settleDelay, b.send)
}

func newNotifier(delay time.Duration, deliver func(Notice)) *Notifier {
	return &Notifier{delay: delay, deliver: deliver}
}

// Show queues a notice. A newer notice arriving within the settle delay takes
// its place.
func (n *Notifier) Show(notice Notice) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return
	}
	n.pending = &notice
	if n.timer != nil {
		n.timer.Stop()
	}
	n.timer = time.AfterFunc(n.delay, n.flush)
}

func (n *Notifier) flush() {
	n.mu.Lock()
	notice := n.pending
	n.pending = nil
	closed := n.closed
	n.mu.Unlock()
	if notice != nil && !closed {
		n.deliver(*notice)
	}
}

// Close drops anything still waiting. Notices shown after Close are ignored.
func (n *Notifier) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed = true
	n.pending = nil
	if n.timer != nil {
		n.timer.Stop()
	}
}

// bus holds the session bus connection and the id of the notification on
// screen. Calls come from one timer at a time, but the lock keeps that true
// even if two flushes overlap.
type bus struct {
	logf func(format string, args ...any)

	mu     sync.Mutex
	conn   *dbus.Conn
	lastID uint32
}

func (b *bus) send(notice Notice) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.conn == nil {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			b.logf("notification not shown, no session bus: %v", err)
			return
		}
		b.conn = conn
	}

	// Calling Notify on a name nobody owns asks the bus to start a server
	// for it. At login that can launch a stray notification daemon before
	// the desktop's own is up, so nobody home means the notice is dropped.
	var owned bool
	if err := b.conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, busName).Store(&owned); err != nil {
		b.reset("notification not shown, the session bus did not answer: %v", err)
		return
	}
	if !owned {
		b.logf("notification not shown, no notification server is running yet")
		return
	}

	hints := map[string]dbus.Variant{
		// Transient keeps device switches out of the notification history.
		"transient": dbus.MakeVariant(true),
		"category":  dbus.MakeVariant("device"),
		// dunst and the servers derived from Ubuntu's notify-osd replace
		// by tag, which covers a server that ignores replaces_id.
		"x-dunst-stack-tag":               dbus.MakeVariant("poweraudio"),
		"x-canonical-private-synchronous": dbus.MakeVariant("poweraudio"),
	}
	var id uint32
	err := b.conn.Object(busName, path).Call(busName+".Notify", 0,
		"poweraudio", b.lastID, notice.Icon, notice.Summary, notice.Body,
		[]string{}, hints, int32(expireMs)).Store(&id)
	if err != nil {
		b.reset("notification not shown: %v", err)
		return
	}
	b.lastID = id
}

// reset drops a connection that failed, so the next notice opens a new one.
func (b *bus) reset(format string, args ...any) {
	b.logf(format, args...)
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
	}
}
