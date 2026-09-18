package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Without a daemon the subscription cannot open, and the UI has to say so and
// keep trying rather than sitting on an empty screen.
func TestSubscriptionRetriesWhenTheDaemonIsAway(t *testing.T) {
	m := NewModel(nil)

	msg := m.Init()()
	failed, ok := msg.(subFailedMsg)
	if !ok {
		t.Fatalf("Init produced %#v, want a failed subscription", msg)
	}

	next, cmd := m.Update(failed)
	m = next.(Model)
	if m.connected {
		t.Error("a failed subscription left the UI looking connected")
	}
	if cmd == nil {
		t.Fatal("a failed subscription did not schedule a retry")
	}
	if m.gen == failed.gen {
		t.Error("the generation did not move on, so the stale stream can come back")
	}
}

func TestSnapshotFeedsEveryScreen(t *testing.T) {
	m := modelAt(t, 90, 24, "")

	if !m.connected {
		t.Error("a snapshot did not mark the daemon connected")
	}
	if got := len(m.devices.devices); got != 5 {
		t.Errorf("the devices screen holds %d devices", got)
	}
	if got := len(m.config.priorities); got != 3 {
		t.Errorf("the config screen holds %d entries", got)
	}
	if got := len(m.status.events); got != 12 {
		t.Errorf("the status screen holds %d events", got)
	}
}

func TestManualRefreshReconnectsOnlyWhenOffline(t *testing.T) {
	m := modelAt(t, 90, 24, "")
	gen := m.gen

	next, _ := m.Update(key("r"))
	if got := next.(Model).gen; got != gen {
		t.Errorf("r opened a second subscription while one was live: gen %d", got)
	}

	next, _ = m.Update(subClosedMsg{gen: gen})
	offline := next.(Model)
	next, cmd := offline.Update(key("r"))
	if got := next.(Model).gen; got == offline.gen {
		t.Error("r did not dial again while offline")
	}
	if cmd == nil {
		t.Error("r produced no work at all")
	}
}

func TestVolumeKeyTravelsThroughTheDebounce(t *testing.T) {
	m := modelAt(t, 90, 24, "d")

	next, cmd := m.Update(key("l"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("a volume key produced no command")
	}

	// The key arms the debounce timer rather than sending straight away.
	tick, ok := cmd().(volumeTickMsg)
	if !ok {
		t.Fatalf("a volume key produced %#v, want a debounce tick", cmd())
	}

	next, cmd = m.Update(tick)
	m = next.(Model)
	req, ok := cmd().(volumeMsg)
	if !ok {
		t.Fatalf("the debounce tick produced %#v, want a request", cmd())
	}
	if req.percent != 60 {
		t.Errorf("the request carries %d%%, want 60%%", req.percent)
	}

	// The request going out and coming back leaves nothing in flight.
	next, _ = m.Update(volumeResultMsg{})
	if m = next.(Model); m.devices.vol.inflight {
		t.Error("a finished request is still counted as in flight")
	}
}

func TestNoticesExpire(t *testing.T) {
	m := modelAt(t, 90, 24, "")

	next, _ := m.Update(noticeMsg{kind: noticeInfo, text: "Priorities saved"})
	m = next.(Model)
	if !strings.Contains(stripANSI(m.View().Content), "Priorities saved") {
		t.Fatal("the notice never reached the status bar")
	}

	next, _ = m.Update(noticeExpiredMsg{at: m.note.at})
	m = next.(Model)
	if strings.Contains(stripANSI(m.View().Content), "Priorities saved") {
		t.Error("the notice outlived its timer")
	}
}

func TestTabAtMapsThePointerToAScreen(t *testing.T) {
	// "d Devices" with two columns of padding each side is thirteen wide,
	// then "c Config" and "s Status" at twelve each.
	cases := []struct {
		x    int
		want screen
		ok   bool
	}{
		{0, screenDevices, true},
		{12, screenDevices, true},
		{13, screenConfig, true},
		{24, screenConfig, true},
		{25, screenStatus, true},
		{36, screenStatus, true},
		{37, screenDevices, false},
	}
	for _, c := range cases {
		got, ok := tabAt(90, c.x)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("tabAt(90, %d) = %v %v, want %v %v", c.x, got, ok, c.want, c.ok)
		}
	}
}

func TestTabsShrinkOnANarrowTerminal(t *testing.T) {
	if got := tabLabels(90); got[0] != "d Devices" {
		t.Errorf("a wide terminal shows %q", got[0])
	}
	if got := tabLabels(20); got[0] != "d" {
		t.Errorf("a narrow terminal shows %q, want the key alone", got[0])
	}
	if _, ok := tabAt(20, 6); !ok {
		t.Error("the compact tabs cannot be clicked")
	}
}

func TestClickOnATabSwitchesScreens(t *testing.T) {
	m := modelAt(t, 90, 24, "d")

	next, _ := m.Update(tea.MouseClickMsg{X: 15, Y: 0, Button: tea.MouseLeft})
	if got := next.(Model).screen; got != screenConfig {
		t.Errorf("a click on the Config tab landed on screen %v", got)
	}
}

func TestWheelScrollsTheListUnderThePointer(t *testing.T) {
	m := modelAt(t, 90, 24, "d")

	next, _ := m.Update(tea.MouseWheelMsg{X: 10, Y: 6, Button: tea.MouseWheelDown})
	if got := next.(Model).devices.cursor; got != 1 {
		t.Errorf("the wheel left the device cursor on %d, want 1", got)
	}

	next, _ = m.Update(key("s"))
	next, _ = next.(Model).Update(tea.MouseWheelMsg{X: 10, Y: 6, Button: tea.MouseWheelDown})
	if got := next.(Model).status.evOffset; got != wheelRows {
		t.Errorf("the wheel scrolled the log to %d, want %d", got, wheelRows)
	}
}

func TestClickOnADeviceRowSelectsIt(t *testing.T) {
	m := modelAt(t, 90, 24, "d")

	row := tabRows + deviceHeaderRows + 3
	next, _ := m.Update(tea.MouseClickMsg{X: 10, Y: row, Button: tea.MouseLeft})
	if got := next.(Model).devices.cursor; got != 3 {
		t.Errorf("a click put the cursor on %d, want 3", got)
	}
}

func TestQuitAsksOnceAboutUnsavedEdits(t *testing.T) {
	m := modelAt(t, 90, 24, "c")

	next, _ := m.Update(key("J"))
	m = next.(Model)
	if !m.config.hasUnsaved() {
		t.Fatal("reordering did not mark the config dirty")
	}

	// The first q warns and stays, so the command it returns is the notice
	// timer rather than a quit.
	next, _ = m.Update(key("q"))
	m = next.(Model)
	if !strings.Contains(stripANSI(m.View().Content), "Unsaved config") {
		t.Error("q did not warn about the unsaved edits")
	}

	_, cmd := m.Update(key("q"))
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("a second q did not quit")
	}
}
