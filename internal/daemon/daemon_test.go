package daemon

import (
	"testing"

	"github.com/roverflow/poweraudio/internal/audio"
)

func TestFindBTDevice(t *testing.T) {
	devices := []audio.Device{
		{ID: "1", Name: "Ryzen HD Audio Controller", Type: audio.DeviceTypeSpeaker},
		{ID: "2", Name: "JBL Tune 520BT", Type: audio.DeviceTypeBluetooth, MACAddress: "3C:B0:ED:3A:2C:42"},
		{ID: "3", Name: "JBL Tune 520BT", Type: audio.DeviceTypeBluetooth, MACAddress: "AA:BB:CC:DD:EE:FF"},
	}

	// Two headsets can report the same model name, so the MAC wins.
	if got := findBTDevice(devices, "AA:BB:CC:DD:EE:FF", "JBL Tune 520BT"); got == nil || got.ID != "3" {
		t.Errorf("MAC lookup picked %v, want the device with that MAC", got)
	}
	if got := findBTDevice(devices, "aa:bb:cc:dd:ee:ff", ""); got == nil || got.ID != "3" {
		t.Errorf("MAC lookup is case sensitive, picked %v", got)
	}

	// Backends that report no MAC fall back to the BlueZ alias.
	if got := findBTDevice(devices, "", "jbl tune"); got == nil || got.ID != "2" {
		t.Errorf("name lookup picked %v, want the first matching sink", got)
	}

	// A speaker whose name happens to match is not a Bluetooth device.
	if got := findBTDevice(devices, "", "Ryzen"); got != nil {
		t.Errorf("matched a non-Bluetooth sink: %v", got)
	}
	if got := findBTDevice(devices, "11:22:33:44:55:66", "Unknown Headset"); got != nil {
		t.Errorf("matched nothing in particular: %v", got)
	}
	if got := findBTDevice(nil, "", ""); got != nil {
		t.Errorf("matched something in an empty list: %v", got)
	}
}
