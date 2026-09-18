package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/roverflow/poweraudio/internal/ipc"
)

func testStatusModel(width, height int) statusModel {
	m := newStatusModel()
	m.width, m.height = width, height
	m.setSnapshot(testSnapshot())
	return m
}

// The log spans days, so a bare clock made yesterday's failure look like this
// morning's.
func TestEventStampCarriesTheDate(t *testing.T) {
	ev := ipc.EventLog{
		Time:    time.Date(2026, 3, 4, 9, 15, 0, 0, time.UTC),
		Level:   ipc.LevelInfo,
		Message: "switched default output",
	}
	row := stripANSI(eventRow(ev, 90))

	if !strings.Contains(row, "Mar 04 09:15:00") {
		t.Errorf("event row = %q, want a dated stamp", row)
	}
	if !strings.Contains(row, "switched default output") {
		t.Errorf("event row lost its message: %q", row)
	}
}

func TestEventColourComesFromTheLevel(t *testing.T) {
	cases := []struct {
		level ipc.Level
		want  string
	}{
		{ipc.LevelDebug, styleMuted.Render("x")},
		{ipc.LevelInfo, styleNormal.Render("x")},
		{ipc.LevelWarn, styleWarn.Render("x")},
		{ipc.LevelError, styleError.Render("x")},
		{ipc.Level("nonsense"), styleNormal.Render("x")},
	}
	for _, c := range cases {
		if got := eventStyle(c.level).Render("x"); got != c.want {
			t.Errorf("eventStyle(%q) rendered %q, want %q", c.level, got, c.want)
		}
	}
}

func TestStatusHeaderCollapsesOnAShortTerminal(t *testing.T) {
	cases := []struct {
		height int
		want   bool
	}{
		{36, true},
		{20, true},
		{13, true},
		{12, false},
		{10, false}, // a fourteen row terminal
	}
	for _, c := range cases {
		if got := statusShowsFields(c.height); got != c.want {
			t.Errorf("statusShowsFields(%d) = %v, want %v", c.height, got, c.want)
		}
	}
}

func TestStatusAlwaysLeavesRoomForEvents(t *testing.T) {
	for _, h := range []int{6, 10, 12, 13, 20, 36} {
		m := testStatusModel(80, h)
		if got := m.eventRows(); got < 1 {
			t.Errorf("at height %d the log gets %d rows", h, got)
		}
		if got := strings.Count(m.View(), "\n") + 1; got != h {
			t.Errorf("at height %d the screen drew %d lines", h, got)
		}
	}
}

func TestStatusShowsTheConfigPath(t *testing.T) {
	m := testStatusModel(90, 20)
	out := stripANSI(m.View())

	if !strings.Contains(out, "/home/someone/.config/poweraudio/config.toml") {
		t.Errorf("the status screen never says where w writes:\n%s", out)
	}
	if strings.Contains(out, "Socket") {
		t.Errorf("the socket line is still there:\n%s", out)
	}
}

// Offering both keys told someone with no unit file that they could remove it.
func TestServiceHintFollowsTheServiceState(t *testing.T) {
	m := testStatusModel(90, 20)

	m.svcInstalled = false
	if got := strings.Join(m.helpHints(), " "); !strings.Contains(got, "i install service") ||
		strings.Contains(got, "u remove service") {
		t.Errorf("with no unit installed the hints are %q", got)
	}

	m.svcInstalled = true
	if got := strings.Join(m.helpHints(), " "); !strings.Contains(got, "u remove service") ||
		strings.Contains(got, "i install service") {
		t.Errorf("with a unit installed the hints are %q", got)
	}
}

func TestServiceKeysOnlyFireWhenTheyApply(t *testing.T) {
	m := testStatusModel(90, 20)

	m.svcInstalled = false
	if _, cmd := m.Update(key("u")); cmd != nil {
		t.Error("u tried to remove a service that is not installed")
	}
	if _, cmd := m.Update(key("i")); cmd == nil {
		t.Error("i did not install the service")
	}

	m.svcInstalled = true
	if _, cmd := m.Update(key("i")); cmd != nil {
		t.Error("i reinstalled a service that is already there")
	}
	if _, cmd := m.Update(key("u")); cmd == nil {
		t.Error("u did not remove the service")
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m 30s"},
		{2*time.Hour + 5*time.Minute, "2h 5m"},
		{-time.Second, "0s"},
	}
	for _, c := range cases {
		if got := formatUptime(c.d); got != c.want {
			t.Errorf("formatUptime(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestUptimeComesFromStartedAt(t *testing.T) {
	m := testStatusModel(90, 20)
	m.status.StartedAt = time.Now().Add(-90 * time.Second)
	if got := m.uptime(); got != "1m 30s" {
		t.Errorf("uptime = %q, want 1m 30s", got)
	}

	m.status.StartedAt = time.Time{}
	if got := m.uptime(); got != "unknown" {
		t.Errorf("uptime with no start time = %q", got)
	}
}
