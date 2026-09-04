package daemon

import (
	"testing"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
)

func sink(name string, t audio.DeviceType) audio.Device {
	return audio.Device{ID: name, Name: name, Type: t, Available: true}
}

func TestMatchesPriority(t *testing.T) {
	headset := audio.Device{
		ID:         "44",
		Name:       "JBL Tune 520BT",
		Type:       audio.DeviceTypeBluetooth,
		MACAddress: "3C:B0:ED:3A:2C:42",
	}

	cases := []struct {
		name string
		prio config.PriorityEntry
		want bool
	}{
		{"substring of the name", config.PriorityEntry{Match: "jbl tune"}, true},
		{"full name", config.PriorityEntry{Match: "JBL Tune 520BT"}, true},
		{"MAC address", config.PriorityEntry{Match: "3c:b0:ed"}, true},
		{"matching type", config.PriorityEntry{Match: "JBL", Type: "bluetooth"}, true},
		{"type spelled differently", config.PriorityEntry{Match: "JBL", Type: "BlueTooth"}, true},
		{"wrong type", config.PriorityEntry{Match: "JBL", Type: "usb"}, false},
		{"different device", config.PriorityEntry{Match: "HDMI"}, false},
		// An empty match would substring-match everything and outrank the
		// entries that were actually configured.
		{"empty match", config.PriorityEntry{}, false},
		{"blank match", config.PriorityEntry{Match: "   "}, false},
		{"type only", config.PriorityEntry{Type: "bluetooth"}, false},
	}

	for _, c := range cases {
		if got := matchesPriority(headset, c.prio); got != c.want {
			t.Errorf("%s: matchesPriority(%+v) = %v, want %v", c.name, c.prio, got, c.want)
		}
	}
}

func TestFindBestDevice(t *testing.T) {
	devices := []audio.Device{
		sink("GA104 Digital Stereo (HDMI)", audio.DeviceTypeHDMI),
		sink("Razer Barracuda X", audio.DeviceTypeUSB),
		sink("Ryzen HD Audio Controller", audio.DeviceTypeSpeaker),
	}

	priorities := []config.PriorityEntry{
		{Match: "JBL Tune 520BT", Type: "bluetooth"},
		{Match: "Razer Barracuda"},
		{Match: "Ryzen HD Audio"},
	}

	got := FindBestDevice(devices, priorities)
	if got == nil || got.Name != "Razer Barracuda X" {
		t.Fatalf("picked %v, want the highest ranked device that is present", got)
	}

	// Nothing on the list is here, so anything beats leaving the output where
	// the session happened to put it.
	got = FindBestDevice(devices, []config.PriorityEntry{{Match: "Nothing Like This"}})
	if got == nil {
		t.Fatal("no device picked when the ranking matched nothing")
	}

	if FindBestDevice(nil, priorities) != nil {
		t.Error("picked a device from an empty list")
	}
}

func TestFindBestDeviceSkipsUnavailable(t *testing.T) {
	devices := []audio.Device{
		{ID: "1", Name: "Razer Barracuda X", Available: false},
		{ID: "2", Name: "Ryzen HD Audio Controller", Available: true},
	}
	priorities := []config.PriorityEntry{{Match: "Razer"}, {Match: "Ryzen"}}

	got := FindBestDevice(devices, priorities)
	if got == nil || got.ID != "2" {
		t.Fatalf("picked %v, want the available device", got)
	}
}

func TestDevicePriority(t *testing.T) {
	priorities := []config.PriorityEntry{{Match: "JBL"}, {Match: "Razer"}}

	if got := DevicePriority(sink("JBL Tune 520BT", audio.DeviceTypeBluetooth), priorities); got != 0 {
		t.Errorf("JBL rank = %d, want 0", got)
	}
	if got := DevicePriority(sink("Razer Barracuda X", audio.DeviceTypeUSB), priorities); got != 1 {
		t.Errorf("Razer rank = %d, want 1", got)
	}
	// Unlisted devices sort behind everything on the list.
	if got := DevicePriority(sink("HDMI", audio.DeviceTypeHDMI), priorities); got != len(priorities) {
		t.Errorf("unlisted rank = %d, want %d", got, len(priorities))
	}
}
