package tui

import (
	"testing"
	"time"
)

func TestVolumeSendsOncePerQuietPeriod(t *testing.T) {
	now := time.Now()
	var v volumeState

	if got := v.edit("sink", 55, now); got != volumeArm {
		t.Fatalf("first edit returned %v, want volumeArm", got)
	}
	seq := v.seq

	// Everything typed inside the quiet period rides on the timer that is
	// already out.
	for _, pct := range []int{60, 65, 70} {
		if got := v.edit("sink", pct, now); got != volumeNothing {
			t.Fatalf("edit to %d%% returned %v, want volumeNothing", pct, got)
		}
	}
	if v.percent != 70 {
		t.Errorf("state holds %d%%, want the last edit 70%%", v.percent)
	}

	if got := v.fire(seq); got != volumeSend {
		t.Fatalf("the timer returned %v, want volumeSend", got)
	}
	if v.percent != 70 || !v.inflight {
		t.Errorf("after sending: percent %d, inflight %v", v.percent, v.inflight)
	}
}

func TestVolumeKeepsOneRequestInFlight(t *testing.T) {
	now := time.Now()
	var v volumeState

	v.edit("sink", 55, now)
	seq := v.seq
	v.fire(seq)

	// A key pressed while the request is out must not start a second one.
	if got := v.edit("sink", 60, now); got != volumeNothing {
		t.Fatalf("edit during a request returned %v, want volumeNothing", got)
	}
	if got := v.fire(v.seq); got != volumeNothing {
		t.Fatalf("a timer during a request returned %v, want volumeNothing", got)
	}

	// The answer coming back re-arms rather than sending straight away, so a
	// held key still costs one request per quiet period.
	if got := v.done(); got != volumeArm {
		t.Fatalf("done with a pending edit returned %v, want volumeArm", got)
	}
	if got := v.fire(v.seq); got != volumeSend {
		t.Fatalf("the re-armed timer returned %v, want volumeSend", got)
	}
	if v.percent != 60 {
		t.Errorf("sent %d%%, want the latest edit 60%%", v.percent)
	}
}

func TestVolumeStaleTimerIsIgnored(t *testing.T) {
	now := time.Now()
	var v volumeState

	v.edit("sink", 55, now)
	stale := v.seq
	v.fire(stale)
	v.done()

	if got := v.fire(stale); got != volumeNothing {
		t.Errorf("a stale timer returned %v, want volumeNothing", got)
	}
}

func TestVolumeHoldsTheLocalLevelUntilASnapshotConfirmsIt(t *testing.T) {
	now := time.Now()
	var v volumeState

	v.edit("sink", 55, now)
	if pct, ok := v.level("sink", now); !ok || pct != 55 {
		t.Fatalf("level right after the edit = %d, %v", pct, ok)
	}
	if _, ok := v.level("other", now); ok {
		t.Error("the hold leaked onto another device")
	}

	// A snapshot that arrives before the request finishes changes nothing.
	v.snapshot()
	if pct, ok := v.level("sink", now); !ok || pct != 55 {
		t.Fatalf("an early snapshot released the hold: %d, %v", pct, ok)
	}

	v.fire(v.seq)
	v.done()
	v.snapshot()
	if _, ok := v.level("sink", now); ok {
		t.Error("the hold survived the snapshot that followed the request")
	}
}

func TestVolumeHoldExpires(t *testing.T) {
	now := time.Now()
	var v volumeState

	v.edit("sink", 55, now)
	if _, ok := v.level("sink", now.Add(volumeHold-time.Millisecond)); !ok {
		t.Error("the hold ended early")
	}
	if _, ok := v.level("sink", now.Add(volumeHold+time.Millisecond)); ok {
		t.Error("a daemon that never answered froze the bar forever")
	}
}

func TestVolumeEditOnAnotherDeviceStartsOver(t *testing.T) {
	now := time.Now()
	var v volumeState

	v.edit("sink", 55, now)
	stale := v.seq
	if got := v.edit("other", 30, now); got != volumeArm {
		t.Fatalf("moving to another device returned %v, want volumeArm", got)
	}
	if v.seq == stale {
		t.Error("the abandoned edit's timer can still fire")
	}
	if got := v.fire(stale); got != volumeNothing {
		t.Errorf("the abandoned timer returned %v, want volumeNothing", got)
	}
}

func TestClampVolumeRange(t *testing.T) {
	cases := []struct{ in, want int }{
		{-20, 0},
		{0, 0},
		{75, 75},
		{150, 150},
		{200, 150},
	}
	for _, c := range cases {
		if got := clampVolume(c.in); got != c.want {
			t.Errorf("clampVolume(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
