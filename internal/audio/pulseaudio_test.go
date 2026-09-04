package audio

import (
	"encoding/json"
	"testing"
)

// A cut-down capture of `pactl -f json list sinks`. The point of keeping it
// verbatim is value_percent: pactl writes it as "40%", and decoding that into
// an int failed the whole document and took the backend down with it.
const pactlSinksSample = `[
  {
    "index": 71,
    "state": "SUSPENDED",
    "name": "alsa_output.usb-Razer_Barracuda_X-01.analog-stereo",
    "description": "Razer Barracuda X Analog Stereo",
    "mute": false,
    "volume": {
      "front-left":  {"value": 26214, "value_percent": "40%", "db": "-23.88 dB"},
      "front-right": {"value": 26214, "value_percent": "40%", "db": "-23.88 dB"}
    },
    "properties": {"device.bus": "usb"}
  },
  {
    "index": 73,
    "state": "RUNNING",
    "name": "alsa_output.pci-0000_0e_00.6.analog-stereo",
    "description": "Ryzen HD Audio Controller Analog Stereo",
    "mute": true,
    "volume": {
      "front-left":  {"value": 29183, "value_percent": "45%", "db": "-21.08 dB"},
      "front-right": {"value": 29183, "value_percent": "45%", "db": "-21.08 dB"}
    },
    "properties": {}
  }
]`

func TestPactlSinksDecode(t *testing.T) {
	var sinks []pactlSink
	if err := json.Unmarshal([]byte(pactlSinksSample), &sinks); err != nil {
		t.Fatalf("decoding pactl output: %v", err)
	}
	if len(sinks) != 2 {
		t.Fatalf("decoded %d sinks, want 2", len(sinks))
	}
	if got := sinks[0].Volume["front-left"].ValuePercent; got != "40%" {
		t.Errorf("value_percent = %q, want %q", got, "40%")
	}
	if !sinks[1].Mute {
		t.Error("second sink should be muted")
	}
}

func TestParsePercent(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"0%", 0},
		{"40%", 0.40},
		{"100%", 1.0},
		{"153%", 1.53},
		{" 45% ", 0.45},
		{"", 1.0}, // unreadable, reported as full rather than silent
		{"loud", 1.0},
	}
	for _, c := range cases {
		if got := parsePercent(c.in); got != c.want {
			t.Errorf("parsePercent(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestClassifyPulseDevice(t *testing.T) {
	cases := []struct {
		desc, name string
		want       DeviceType
	}{
		{"JBL Tune 520BT", "bluez_output.3C_B0_ED.1", DeviceTypeBluetooth},
		{"GA104 Digital Stereo (HDMI)", "alsa_output.pci.hdmi-stereo", DeviceTypeHDMI},
		{"Razer Barracuda X", "alsa_output.usb-Razer-01.analog-stereo", DeviceTypeUSB},
		{"Ryzen HD Audio Controller", "alsa_output.pci-0000_0e.analog-stereo", DeviceTypeSpeaker},
	}
	for _, c := range cases {
		got := classifyPulseDevice(pactlSink{Description: c.desc, Name: c.name})
		if got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.desc, got, c.want)
		}
	}
}
