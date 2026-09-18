package tui

import "time"

const (
	// volumeDebounce is the quiet period an edit waits out before the UI
	// sends it. A held arrow key repeats far faster than a round trip to the
	// daemon, and one request per repeat used to queue up behind itself until
	// the bar crawled seconds behind the key.
	volumeDebounce = 60 * time.Millisecond

	// volumeHold is how long the locally edited level wins over the value in
	// the snapshot when no snapshot ever confirms the change. Without the
	// deadline a request the daemon silently dropped would freeze the bar.
	volumeHold = 2 * time.Second

	volumeMin  = 0
	volumeMax  = 150
	volumeStep = 5
	volumeFine = 1
)

// volumeAction is what the caller has to do after a state transition. The
// state machine owns the decision; the model owns the commands, so the whole
// coalescing rule is testable without a daemon or a clock.
type volumeAction int

const (
	volumeNothing volumeAction = iota

	// volumeArm asks for a debounce timer carrying the current seq.
	volumeArm

	// volumeSend asks for one request with the current id and percent.
	volumeSend
)

// volumeState coalesces volume edits into at most one request per quiet
// period, with never more than one in flight. The level shown on screen is
// the edited one from the first keypress, so the bar tracks the key and never
// jumps backwards to a value the daemon has not caught up with yet.
type volumeState struct {
	id      string
	percent int

	// seq invalidates the timer of an edit that has already been superseded,
	// since a tea.Tick cannot be cancelled once it is out.
	seq int

	armed    bool
	inflight bool
	dirty    bool

	// settled means the last request finished and nothing changed since, so
	// the next snapshot is the one that confirms it and releases the hold.
	settled bool

	holdTo time.Time
}

func clampVolume(percent int) int {
	if percent > volumeMax {
		return volumeMax
	}
	if percent < volumeMin {
		return volumeMin
	}
	return percent
}

// edit records a new local level for a device and reports what to do next.
func (v *volumeState) edit(id string, percent int, now time.Time) volumeAction {
	if v.id != id {
		// Moving to another device abandons the previous edit, but the seq
		// carries over so its timer still lands on a stale sequence number.
		*v = volumeState{seq: v.seq}
	}
	v.id = id
	v.percent = clampVolume(percent)
	v.dirty = true
	v.settled = false
	v.holdTo = now.Add(volumeHold)

	if v.armed || v.inflight {
		return volumeNothing
	}
	v.armed = true
	v.seq++
	return volumeArm
}

// fire runs when the debounce timer for seq expires.
func (v *volumeState) fire(seq int) volumeAction {
	if !v.armed || seq != v.seq {
		return volumeNothing
	}
	v.armed = false
	if v.inflight || !v.dirty {
		return volumeNothing
	}
	v.dirty = false
	v.inflight = true
	return volumeSend
}

// done runs when a request comes back. A level edited while the request was
// out re-arms the timer instead of going straight back out, so holding a key
// still costs one request per quiet period rather than one per round trip.
func (v *volumeState) done() volumeAction {
	v.inflight = false
	if v.dirty {
		if v.armed {
			return volumeNothing
		}
		v.armed = true
		v.seq++
		return volumeArm
	}
	if !v.armed {
		v.settled = true
	}
	return volumeNothing
}

// snapshot releases the local level once a finished request has been followed
// by a fresh snapshot, which is the first one that can contain the new value.
func (v *volumeState) snapshot() {
	if v.settled {
		*v = volumeState{seq: v.seq}
	}
}

// level is the percentage to draw for a device, and whether there is a local
// edit to draw at all.
func (v *volumeState) level(id string, now time.Time) (int, bool) {
	if v.id == "" || v.id != id || now.After(v.holdTo) {
		return 0, false
	}
	return v.percent, true
}
