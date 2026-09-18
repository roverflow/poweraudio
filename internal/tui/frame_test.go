package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]|\x1b\\][^\x07]*\x07")

func stripANSI(s string) string {
	return ansi.ReplaceAllString(s, "")
}

func key(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: s, Code: []rune(s)[0]}
}

// testSnapshot is a daemon at rest: a handful of sinks, a ranking that covers
// some of them, and a log long enough to scroll.
func testSnapshot() ipc.Snapshot {
	start := time.Date(2026, 3, 4, 9, 15, 0, 0, time.UTC)

	devices := []audio.Device{
		{ID: "alsa_output.pci-0000_00_1f.3.analog-stereo", Name: "Built-in Audio Analog Stereo",
			Description: "alsa_output.pci-0000_00_1f.3.analog-stereo", Type: audio.DeviceTypeSpeaker,
			Available: true, Volume: 0.55, IsDefault: true},
		{ID: "bluez_output.3C_B0_ED_3A_2C_42.1", Name: "JBL Tune 520BT",
			Description: "bluez_output.3C_B0_ED_3A_2C_42.1", Type: audio.DeviceTypeBluetooth,
			Available: true, Volume: 0.8, MACAddress: "3C:B0:ED:3A:2C:42"},
		{ID: "alsa_output.usb-1532_Razer_Barracuda_X-00.analog-stereo", Name: "Razer Barracuda X",
			Description: "alsa_output.usb-1532_Razer_Barracuda_X-00.analog-stereo",
			Type:        audio.DeviceTypeUSB, Available: true, Volume: 1.2},
		{ID: "alsa_output.pci-0000_01_00.1.hdmi-stereo", Name: "GP106 High Definition Audio Controller Digital Stereo (HDMI)",
			Description: "alsa_output.pci-0000_01_00.1.hdmi-stereo", Type: audio.DeviceTypeHDMI,
			Available: true, Volume: 0.4, Muted: true},
		{ID: "alsa_output.usb-Generic_Headset-00.analog-stereo", Name: "Generic Headset",
			Description: "alsa_output.usb-Generic_Headset-00.analog-stereo", Type: audio.DeviceTypeHeadphone,
			Available: true, Volume: 0.65},
	}

	events := make([]ipc.EventLog, 0, 12)
	levels := []ipc.Level{ipc.LevelDebug, ipc.LevelInfo, ipc.LevelWarn, ipc.LevelError}
	for i := 0; i < 12; i++ {
		events = append(events, ipc.EventLog{
			Time:    start.Add(time.Duration(i) * time.Minute),
			Level:   levels[i%len(levels)],
			Message: "switched default output to JBL Tune 520BT after a connect",
		})
	}

	return ipc.Snapshot{
		Devices: devices,
		Status: ipc.StatusData{
			Backend:    "pipewire",
			ConfigPath: "/home/someone/.config/poweraudio/config.toml",
			StartedAt:  start,
		},
		Events: events,
		Config: config.Config{
			Switching: config.SwitchingConfig{OnConnect: "priority", OnDisconnect: "priority"},
			Priority: []config.PriorityEntry{
				{Match: "JBL Tune 520BT", Type: "bluetooth"},
				{Match: "razer"},
				{Match: "Built-in Audio Analog Stereo"},
			},
		},
	}
}

// modelAt is a model that has been sized, fed one snapshot and put on a screen.
func modelAt(t *testing.T, width, height int, screenKey string) Model {
	t.Helper()

	snap := testSnapshot()
	var m tea.Model = NewModel(nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m, _ = m.Update(refreshMsg{snap: &snap})
	if screenKey != "" {
		m, _ = m.Update(key(screenKey))
	}
	return m.(Model)
}

func TestFrameFitsEveryTerminalSize(t *testing.T) {
	sizes := []struct{ w, h int }{
		{90, 24},
		{120, 40},
		{50, 14},
		{60, 16},
		{38, 10},
	}
	screens := []struct{ name, key string }{
		{"devices", "d"},
		{"config", "c"},
		{"status", "s"},
		{"help", "?"},
	}

	for _, size := range sizes {
		for _, sc := range screens {
			m := modelAt(t, size.w, size.h, sc.key)
			lines := strings.Split(m.View().Content, "\n")

			if len(lines) != size.h {
				t.Errorf("%s at %dx%d produced %d lines, want %d",
					sc.name, size.w, size.h, len(lines), size.h)
			}
			for i, line := range lines {
				if got := runeLen(stripANSI(line)); got > size.w {
					t.Errorf("%s at %dx%d line %d is %d columns wide: %q",
						sc.name, size.w, size.h, i, got, stripANSI(line))
				}
			}
		}
	}
}

func TestConfigSwitchingSectionFitsEveryTerminalSize(t *testing.T) {
	for _, size := range []struct{ w, h int }{{90, 24}, {50, 14}, {38, 10}} {
		m := modelAt(t, size.w, size.h, "c")
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m = next.(Model)

		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != size.h {
			t.Errorf("switching at %dx%d produced %d lines, want %d",
				size.w, size.h, len(lines), size.h)
		}
		for i, line := range lines {
			if got := runeLen(stripANSI(line)); got > size.w {
				t.Errorf("switching at %dx%d line %d is %d columns wide: %q",
					size.w, size.h, i, got, stripANSI(line))
			}
		}
	}
}

func TestHelpShowsTheStatusKeysOnAShortTerminal(t *testing.T) {
	m := modelAt(t, 90, 24, "?")
	out := stripANSI(m.View().Content)

	if !strings.Contains(out, "Status") {
		t.Errorf("the help screen clipped the Status section:\n%s", out)
	}
	if !strings.Contains(out, "install or remove the service") {
		t.Errorf("the help screen clipped the service keys:\n%s", out)
	}
}

func TestStatusKeepsTheLogAndTheHelpLineWhenShort(t *testing.T) {
	m := modelAt(t, 50, 14, "s")
	out := stripANSI(m.View().Content)

	if !strings.Contains(out, "Mar 04") {
		t.Errorf("no event row survived on a short terminal:\n%s", out)
	}
	if !strings.Contains(out, "j/k scroll") {
		t.Errorf("the help line was clipped on a short terminal:\n%s", out)
	}
}

func TestStatusBarShowsTheDaemonState(t *testing.T) {
	m := modelAt(t, 90, 24, "")
	if out := stripANSI(m.View().Content); !strings.Contains(out, "daemon ready") {
		t.Errorf("a fed model does not report a ready daemon:\n%s", out)
	}

	next, _ := m.Update(subClosedMsg{gen: m.gen})
	m = next.(Model)
	if out := stripANSI(m.View().Content); !strings.Contains(out, "daemon offline") {
		t.Errorf("a dropped subscription does not report an offline daemon:\n%s", out)
	}
}

// A retry timer from a generation a manual reconnect has overtaken must not
// open a second subscription.
func TestStaleSubscriptionMessagesAreDropped(t *testing.T) {
	m := modelAt(t, 90, 24, "")
	gen := m.gen

	next, cmd := m.Update(subRetryMsg{gen: gen - 1})
	if cmd != nil {
		t.Error("a retry from an older generation reconnected")
	}
	m = next.(Model)

	snap := testSnapshot()
	snap.Devices = nil
	next, _ = m.Update(snapshotMsg{gen: gen - 1, snap: snap})
	if got := len(next.(Model).devices.devices); got != 5 {
		t.Errorf("a snapshot from an older generation was applied: %d devices left", got)
	}
}
