package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDaemonLogPathFollowsXDGState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	if got, want := daemonLogPath(), "/tmp/state/poweraudio/daemon.log"; got != want {
		t.Errorf("daemonLogPath = %q, want %q", got, want)
	}

	t.Setenv("XDG_STATE_HOME", "")
	home, _ := os.UserHomeDir()
	if got, want := daemonLogPath(), filepath.Join(home, ".local", "state", "poweraudio", "daemon.log"); got != want {
		t.Errorf("daemonLogPath with no XDG_STATE_HOME = %q, want %q", got, want)
	}
}

func TestOpenDaemonLogAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "daemon.log")

	f, err := openDaemonLog(path)
	if err != nil {
		t.Fatalf("openDaemonLog: %v", err)
	}
	f.WriteString("first\n")
	f.Close()

	f, err = openDaemonLog(path)
	if err != nil {
		t.Fatalf("second openDaemonLog: %v", err)
	}
	f.WriteString("second\n")
	f.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if string(data) != "first\nsecond\n" {
		t.Errorf("the log holds %q, want both runs", data)
	}
}

func TestSetupScreenFollowsTheTerminalWidth(t *testing.T) {
	var m tea.Model = NewSetupModel(nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 44, Height: 12})

	for _, line := range strings.Split(m.(SetupModel).View().Content, "\n") {
		if got := runeLen(stripANSI(line)); got > 44 {
			t.Errorf("a setup line is %d columns wide: %q", got, stripANSI(line))
		}
	}
}
