package tui

import "time"

const (
	// volumeDebounce is the quiet period before the UI sends an edit. A held
	// key repeats faster than a daemon round trip.
	volumeDebounce = 60 * time.Millisecond

	// volumeHold caps how long a local edit overrides the snapshot, so a
	// request the daemon dropped cannot freeze the bar.
	volumeHold = 2 * time.Second

	volumeMin  = 0
	volumeMax  = 150
	volumeStep = 5
	volumeFine = 1
)

// volumeAction tells the model which command to issue, which keeps the
// state machine testable without a daemon or a clock.
type volumeAction int

const (
	volumeNothing volumeAction = iota

	// volumeArm asks for a debounce timer carrying the current seq.
	volumeArm

	// volumeSend asks for one request with the current id and percent.
	volumeSend
)

// volumeState coalesces edits into at most one request per quiet period,
// with at most one in flight. The bar shows the local level meanwhile.
type volumeState struct {
	id      string
	percent int

	// seq invalidates the timer of a superseded edit, since a tea.Tick cannot
	// be cancelled once it is out.
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

// done re-arms the timer for an edit made while the request was out, so a
// held key costs one request per quiet period, not one per round trip.
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

// snapshot releases the local level once a snapshot follows a finished
// request, since that is the first one that can hold the new value.
func (v *volumeState) snapshot() {
	if v.settled {
		*v = volumeState{seq: v.seq}
	}
}

// level reports the local edit for id, if there is one to draw.
func (v *volumeState) level(id string, now time.Time) (int, bool) {
	if v.id == "" || v.id != id || now.After(v.holdTo) {
		return 0, false
	}
	return v.percent, true
}
