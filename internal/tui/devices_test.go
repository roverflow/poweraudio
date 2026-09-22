package tui

import (
	"strings"
	"testing"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
)

func testDevicesModel(width, height int) devicesModel {
	snap := testSnapshot()
	m := newDevicesModel()
	m.width, m.height = width, height
	m.setSnapshot(snap.Devices, snap.Config.Priority)
	return m
}

func TestDeviceRowsNeverOutgrowTheTerminal(t *testing.T) {
	for _, w := range []int{24, 30, 38, 50, 60, 90, 120} {
		m := testDevicesModel(w, 20)
		nameW, typeW, barW := deviceColumns(w)

		for i, dev := range m.devices {
			row := stripANSI(m.row(i, dev, w, nameW, typeW, barW))
			if got := runeLen(row); got > w-1 {
				t.Errorf("at width %d a row is %d columns: %q", w, got, row)
			}
		}
	}
}

func TestDeviceColumnsDropTheTypeBeforeTheName(t *testing.T) {
	if _, typeW, _ := deviceColumns(90); typeW != deviceTypeW {
		t.Errorf("a wide terminal dropped the type column")
	}
	if _, typeW, _ := deviceColumns(38); typeW != 0 {
		t.Errorf("a narrow terminal kept the type column")
	}
	for _, w := range []int{20, 24, 38, 90} {
		if nameW, _, _ := deviceColumns(w); nameW < 1 {
			t.Errorf("deviceColumns(%d) left no room for a name", w)
		}
	}
}

func TestDevicePanelFits(t *testing.T) {
	cases := []struct {
		height, devices int
		want            bool
	}{
		{20, 5, true},   // six rows spare
		{15, 5, false},  // five rows spare, not enough for the panel
		{36, 5, true},   // plenty
		{20, 12, false}, // a long list leaves nothing
		{20, 0, false},  // nothing to describe
	}
	for _, c := range cases {
		if got := devicePanelFits(c.height, c.devices); got != c.want {
			t.Errorf("devicePanelFits(%d, %d) = %v, want %v",
				c.height, c.devices, got, c.want)
		}
	}
}

func TestDetailRowsDescribeTheSelectedDevice(t *testing.T) {
	snap := testSnapshot()
	entries := snap.Config.Priority

	rows := detailRows(snap.Devices[1], 80, entries, 90)
	joined := stripANSI(strings.Join(rows, "\n"))

	for _, want := range []string{
		"bluez_output.3C_B0_ED_3A_2C_42.1",
		"Bluetooth",
		"3C:B0:ED:3A:2C:42",
		"80%",
		"ranked 1 of 3",
		"default no",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the panel is missing %q:\n%s", want, joined)
		}
	}

	down := snap.Devices[1]
	down.Available = false
	rows = detailRows(down, 80, entries, 90)
	if joined = stripANSI(strings.Join(rows, "\n")); !strings.Contains(joined, "unavailable") {
		t.Errorf("a sink that cannot play says nothing about it:\n%s", joined)
	}

	// A device no entry matches says so rather than claiming the bottom rank.
	unranked := audio.Device{ID: "x", Name: "Webcam Audio", Description: "alsa_output.webcam"}
	rows = detailRows(unranked, 40, entries, 90)
	if joined = stripANSI(strings.Join(rows, "\n")); !strings.Contains(joined, "not on the priority list") {
		t.Errorf("an unranked device was given a rank:\n%s", joined)
	}
}

// The panel follows the cursor, which is the only way to inspect a device
// other than the one that happens to be playing.
func TestDetailPanelFollowsTheCursor(t *testing.T) {
	m := testDevicesModel(90, 30)
	if !m.showPanel() {
		t.Fatal("there is room for the panel but it is not drawn")
	}
	if got := stripANSI(m.View()); !strings.Contains(got, m.devices[0].Description) {
		t.Errorf("the panel does not describe the first device:\n%s", got)
	}

	m, _ = m.handleKey(key("j"))
	m, _ = m.handleKey(key("j"))
	if got := stripANSI(m.View()); !strings.Contains(got, m.devices[2].Description) {
		t.Errorf("the panel did not follow the cursor:\n%s", got)
	}
}

func TestDeviceRowIndexSkipsTheHeaderAndThePanel(t *testing.T) {
	m := testDevicesModel(90, 30)

	if got := m.rowIndex(deviceHeaderRows); got != 0 {
		t.Errorf("the first list row mapped to %d, want 0", got)
	}
	if got := m.rowIndex(deviceHeaderRows + 3); got != 3 {
		t.Errorf("the fourth list row mapped to %d, want 3", got)
	}
	if got := m.rowIndex(0); got != -1 {
		t.Errorf("a click on the title mapped to %d, want -1", got)
	}
	if got := m.rowIndex(deviceHeaderRows + len(m.devices)); got != -1 {
		t.Errorf("a click past the last device mapped to %d, want -1", got)
	}
}

func TestClickSelectsThenPlays(t *testing.T) {
	m := testDevicesModel(90, 30)

	m, cmd := m.click(deviceHeaderRows + 2)
	if m.cursor != 2 {
		t.Fatalf("the cursor is on %d, want 2", m.cursor)
	}
	if cmd != nil {
		t.Fatal("the first click on a row already switched the output")
	}

	_, cmd = m.click(deviceHeaderRows + 2)
	if cmd == nil {
		t.Fatal("a second click on the selected row did nothing")
	}
	msg, ok := cmd().(switchDeviceMsg)
	if !ok || msg.deviceID != m.devices[2].ID {
		t.Errorf("a second click produced %#v", cmd())
	}
}

func TestVolumeKeysMoveTheBarImmediately(t *testing.T) {
	m := testDevicesModel(90, 30)
	dev := m.devices[0]
	start := m.volumeOf(dev)

	m, _ = m.handleKey(key("l"))
	if got := m.volumeOf(dev); got != start+volumeStep {
		t.Errorf("l moved the level to %d, want %d", got, start+volumeStep)
	}

	m, _ = m.handleKey(key("H"))
	if got := m.volumeOf(dev); got != start+volumeStep-volumeFine {
		t.Errorf("H moved the level to %d, want %d", got, start+volumeStep-volumeFine)
	}
}

// A snapshot arriving between the keypress and the answer used to redraw the
// bar at the daemon's older level, so the level appeared to jump backwards.
func TestSnapshotDoesNotDragThePendingLevelBack(t *testing.T) {
	snap := testSnapshot()
	m := testDevicesModel(90, 30)

	m, _ = m.handleKey(key("l"))
	pending := m.volumeOf(m.devices[0])

	m.setSnapshot(snap.Devices, snap.Config.Priority)
	if got := m.volumeOf(m.devices[0]); got != pending {
		t.Errorf("the level fell back to %d, want the pending %d", got, pending)
	}
}

func TestRankLabel(t *testing.T) {
	entries := []config.PriorityEntry{{Match: "JBL"}, {Match: "razer"}}
	dev := audio.Device{Name: "Razer Barracuda X"}

	if got := rankLabel(dev, entries); got != "ranked 2 of 2" {
		t.Errorf("rankLabel = %q, want %q", got, "ranked 2 of 2")
	}
	if got := rankLabel(audio.Device{Name: "Webcam"}, entries); got != "not on the priority list" {
		t.Errorf("rankLabel for an unranked device = %q", got)
	}
}
