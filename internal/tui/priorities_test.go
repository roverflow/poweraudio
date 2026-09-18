package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/config"
)

func testConfigModel(width, height int) configModel {
	snap := testSnapshot()
	m := newConfigModel()
	m.width, m.height = width, height
	m.setSnapshot(snap.Devices, snap.Config)
	return m
}

func TestTypeLabelIsTheSameOnBothLists(t *testing.T) {
	cases := []struct{ in, want string }{
		{"bluetooth", "Bluetooth"},
		{"Bluetooth", "Bluetooth"},
		{"hdmi", "HDMI"},
		{"HDMI", "HDMI"},
		{"usb", "USB"},
		{"speaker", "Speaker"},
		{"headphone", "Headphone"},
		{"", "any"},
		{"  ", "any"},
		{"gramophone", "Gramophone"},
	}
	for _, c := range cases {
		if got := typeLabel(c.in); got != c.want {
			t.Errorf("typeLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The UI compared names exactly while the daemon matched a substring, so a
// hand-written entry like "razer" switched correctly and still showed the
// headset as unranked with no presence dot.
func TestBothListsUseTheSharedMatcher(t *testing.T) {
	m := testConfigModel(90, 20)

	for _, dev := range m.availableDevices() {
		if strings.Contains(strings.ToLower(dev.Name), "razer") {
			t.Errorf("%q is ranked by the entry \"razer\" but sits under Available", dev.Name)
		}
	}

	out := stripANSI(m.View())
	if !strings.Contains(out, "●  2. razer") {
		t.Errorf("the entry \"razer\" has no presence dot:\n%s", out)
	}
}

func TestEnterOnARankedEntryPlaysTheFirstPresentMatch(t *testing.T) {
	m := testConfigModel(90, 20)
	m.prioFocus, m.prioCursor = focusPriorityList, 1 // the "razer" entry

	_, cmd := m.activate()
	if cmd == nil {
		t.Fatal("enter on a ranked entry did nothing")
	}
	msg, ok := cmd().(switchDeviceMsg)
	if !ok {
		t.Fatalf("enter produced %#v, want a switch", cmd())
	}
	if !strings.Contains(msg.deviceID, "Razer") {
		t.Errorf("enter switched to %q, want the Razer sink", msg.deviceID)
	}
}

func TestEnterOnAnAbsentEntrySaysSo(t *testing.T) {
	m := testConfigModel(90, 20)
	m.priorities = append(m.priorities, config.PriorityEntry{Match: "Sony WH-1000XM4"})
	m.prioFocus, m.prioCursor = focusPriorityList, len(m.priorities)-1

	_, cmd := m.activate()
	if cmd == nil {
		t.Fatal("enter on an absent entry did nothing")
	}
	msg, ok := cmd().(noticeMsg)
	if !ok {
		t.Fatalf("enter produced %#v, want a notice", cmd())
	}
	if msg.text != "Sony WH-1000XM4 is not connected" {
		t.Errorf("notice = %q", msg.text)
	}
}

func TestEnterOnAnAvailableDeviceAddsIt(t *testing.T) {
	m := testConfigModel(90, 20)
	before := len(m.priorities)
	m.prioFocus, m.availCursor = focusAvailableList, 0
	dev := m.availableDevices()[0]

	m, _ = m.activate()
	if len(m.priorities) != before+1 {
		t.Fatalf("the ranking holds %d entries, want %d", len(m.priorities), before+1)
	}
	added := m.priorities[len(m.priorities)-1]
	if added.Match != dev.Name {
		t.Errorf("added %q, want %q", added.Match, dev.Name)
	}
	if !m.dirty {
		t.Error("adding an entry left the screen looking saved")
	}
}

func TestPriorityModeWarning(t *testing.T) {
	const want = "needs at least one priority entry, otherwise nothing ever switches"

	if got := priorityModeWarning("priority", 0); got != want {
		t.Errorf("priority mode with an empty ranking = %q", got)
	}
	if got := priorityModeWarning("priority", 2); got != "" {
		t.Errorf("priority mode with a ranking = %q, want no warning", got)
	}
	if got := priorityModeWarning("always", 0); got != "" {
		t.Errorf("always mode = %q, want no warning", got)
	}
}

func TestSwitchingSectionShowsTheWarning(t *testing.T) {
	m := testConfigModel(90, 20)
	m.priorities = nil
	m.switching.OnConnect = "priority"
	m.section = sectionSwitching

	if out := stripANSI(m.View()); !strings.Contains(out, "needs at least one priority entry") {
		t.Errorf("priority mode with no ranking carries no warning:\n%s", out)
	}
}

func TestConfigRowTargetMapsTheFlatList(t *testing.T) {
	m := testConfigModel(90, 20)
	n := len(m.priorities)

	if kind, idx := m.rowTarget(1); kind != configRowPriority || idx != 1 {
		t.Errorf("row 1 mapped to %v %d, want a priority entry", kind, idx)
	}
	if kind, _ := m.rowTarget(n); kind != configRowNone {
		t.Errorf("the blank between the lists mapped to %v", kind)
	}
	if kind, _ := m.rowTarget(n + 1); kind != configRowNone {
		t.Errorf("the Available heading mapped to %v", kind)
	}
	if kind, idx := m.rowTarget(n + 2); kind != configRowAvailable || idx != 0 {
		t.Errorf("the first available device mapped to %v %d", kind, idx)
	}
}

func TestClickMovesTheConfigCursor(t *testing.T) {
	m := testConfigModel(90, 20)
	n := len(m.priorities)

	m, _ = m.click(configHeaderRows + n + 2)
	if m.prioFocus != focusAvailableList || m.availCursor != 0 {
		t.Errorf("a click on the available list left focus %v cursor %d", m.prioFocus, m.availCursor)
	}

	m, _ = m.click(configHeaderRows + 2)
	if m.prioFocus != focusPriorityList || m.prioCursor != 2 {
		t.Errorf("a click on the ranking left focus %v cursor %d", m.prioFocus, m.prioCursor)
	}
}

func TestUnsavedEditsSurviveASnapshot(t *testing.T) {
	snap := testSnapshot()
	m := testConfigModel(90, 20)

	m, _ = m.handlePriorityInput(key("J"))
	reordered := m.priorities[0].Match

	m.setSnapshot(snap.Devices, snap.Config)
	if m.priorities[0].Match != reordered {
		t.Errorf("a snapshot threw away an unsaved reordering: %q", m.priorities[0].Match)
	}
	if !m.hasUnsaved() {
		t.Error("the screen forgot that it has unsaved edits")
	}
}

func TestSwitchingKeysPickAnOption(t *testing.T) {
	m := testConfigModel(90, 20)
	m.section = sectionSwitching
	m.switchCursor = 2 // never

	m, _ = m.handleSwitchingInput(tea.KeyPressMsg{Code: ' '})
	if m.switching.OnConnect != "never" {
		t.Errorf("on_connect = %q, want never", m.switching.OnConnect)
	}
	if !m.switchDirty {
		t.Error("picking an option left the screen looking saved")
	}
}
