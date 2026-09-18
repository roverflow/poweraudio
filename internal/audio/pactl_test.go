package audio

import "testing"

// A capture of `pactl -f json list sinks` from a PipeWire 1.6.8 machine, cut
// down to the properties this package reads and kept verbatim otherwise. Two
// entries are hand written: pipewire-pulse only publishes a bluez5 sink while
// headphones are connected, and nothing on this machine sets
// device.form.factor, so neither path would ever be covered by a live capture.
//
// value_percent is the reason the sample stays literal. pactl writes it as
// "40%", and decoding that into an int fails the whole document, which takes
// the backend down rather than one sink.
const pactlSinksSample = `[
  {
    "index": 68,
    "state": "SUSPENDED",
    "name": "alsa_output.pci-0000_01_00.1.hdmi-stereo",
    "description": "GA104 High Definition Audio Controller Digital Stereo (HDMI)",
    "mute": false,
    "volume": {
      "front-left":  {"value": 26214, "value_percent": "40%", "db": "-23.88 dB"},
      "front-right": {"value": 26214, "value_percent": "40%", "db": "-23.88 dB"}
    },
    "properties": {
      "device.bus": "pci",
      "device.api": "alsa",
      "media.class": "Audio/Sink",
      "node.name": "alsa_output.pci-0000_01_00.1.hdmi-stereo",
      "node.nick": "BenQ GW2790Q",
      "device.description": "GA104 High Definition Audio Controller",
      "object.id": "63"
    }
  },
  {
    "index": 73,
    "state": "SUSPENDED",
    "name": "alsa_output.usb-1532_Razer_Barracuda_X_R002000000-01.analog-stereo",
    "description": "Razer Barracuda X Analog Stereo",
    "mute": false,
    "volume": {
      "front-left":  {"value": 29491, "value_percent": "45%", "db": "-20.81 dB"},
      "front-right": {"value": 29491, "value_percent": "45%", "db": "-20.81 dB"}
    },
    "properties": {
      "device.bus": "usb",
      "device.api": "alsa",
      "media.class": "Audio/Sink",
      "node.name": "alsa_output.usb-1532_Razer_Barracuda_X_R002000000-01.analog-stereo",
      "device.description": "Razer Barracuda X",
      "object.id": "45"
    }
  },
  {
    "index": 75,
    "state": "RUNNING",
    "name": "alsa_output.pci-0000_0e_00.6.analog-stereo",
    "description": "Ryzen HD Audio Controller Analog Stereo",
    "mute": true,
    "volume": {
      "front-left":  {"value": 34734, "value_percent": "53%", "db": "-16.54 dB"},
      "front-right": {"value": 34734, "value_percent": "53%", "db": "-16.54 dB"}
    },
    "properties": {
      "device.bus": "pci",
      "device.api": "alsa",
      "media.class": "Audio/Sink",
      "node.name": "alsa_output.pci-0000_0e_00.6.analog-stereo",
      "device.description": "Ryzen HD Audio Controller",
      "object.id": "48"
    }
  },
  {
    "index": 81,
    "state": "SUSPENDED",
    "name": "bluez_output.3C_B0_ED_3A_2C_42.1",
    "description": "JBL Tune 520BT",
    "mute": false,
    "volume": {
      "mono": {"value": 65536, "value_percent": "100%", "db": "0.00 dB"}
    },
    "properties": {
      "device.api": "bluez5",
      "api.bluez5.address": "3C:B0:ED:3A:2C:42",
      "api.bluez5.profile": "a2dp-sink",
      "media.class": "Audio/Sink",
      "node.name": "bluez_output.3C_B0_ED_3A_2C_42.1",
      "device.description": "JBL Tune 520BT",
      "object.id": "91"
    }
  },
  {
    "index": 82,
    "state": "SUSPENDED",
    "name": "alsa_output.usb-Generic_Headset-00.analog-stereo",
    "description": "Generic Headset Analog Stereo",
    "mute": false,
    "volume": {
      "front-left":  {"value": 45875, "value_percent": "70%", "db": "-9.29 dB"},
      "front-right": {"value": 45875, "value_percent": "70%", "db": "-9.29 dB"}
    },
    "properties": {
      "device.bus": "usb",
      "device.api": "alsa",
      "device.form.factor": "headset",
      "media.class": "Audio/Sink",
      "node.name": "alsa_output.usb-Generic_Headset-00.analog-stereo",
      "device.description": "Generic Headset",
      "object.id": "92"
    }
  }
]`

func decodeSample(t *testing.T) []pactlSink {
	t.Helper()
	sinks, err := decodeSinks([]byte(pactlSinksSample))
	if err != nil {
		t.Fatalf("decoding pactl output: %v", err)
	}
	if len(sinks) != 5 {
		t.Fatalf("decoded %d sinks, want 5", len(sinks))
	}
	return sinks
}

func TestDecodeSinks(t *testing.T) {
	sinks := decodeSample(t)

	if got := sinks[0].Volume["front-left"].ValuePercent; got != "40%" {
		t.Errorf("value_percent = %q, want %q", got, "40%")
	}
	if !sinks[2].Mute {
		t.Error("the Ryzen sink should be muted")
	}
	if got := sinks[3].Properties["api.bluez5.address"]; got != "3C:B0:ED:3A:2C:42" {
		t.Errorf("bluez address = %q, want %q", got, "3C:B0:ED:3A:2C:42")
	}
}

func TestDecodeSinksRejectsGarbage(t *testing.T) {
	if _, err := decodeSinks([]byte("Failure: Connection refused")); err == nil {
		t.Error("decoding a non-JSON pactl failure should report an error")
	}
}

func TestDevicesFrom(t *testing.T) {
	devices := devicesFrom(decodeSample(t), "alsa_output.pci-0000_0e_00.6.analog-stereo")

	want := []Device{
		{
			ID:          "alsa_output.pci-0000_01_00.1.hdmi-stereo",
			Name:        "GA104 High Definition Audio Controller Digital Stereo (HDMI)",
			Description: "alsa_output.pci-0000_01_00.1.hdmi-stereo",
			Type:        DeviceTypeHDMI,
			Available:   true,
			Volume:      0.40,
		},
		{
			ID:          "alsa_output.usb-1532_Razer_Barracuda_X_R002000000-01.analog-stereo",
			Name:        "Razer Barracuda X Analog Stereo",
			Description: "alsa_output.usb-1532_Razer_Barracuda_X_R002000000-01.analog-stereo",
			Type:        DeviceTypeUSB,
			Available:   true,
			Volume:      0.45,
		},
		{
			ID:          "alsa_output.pci-0000_0e_00.6.analog-stereo",
			Name:        "Ryzen HD Audio Controller Analog Stereo",
			Description: "alsa_output.pci-0000_0e_00.6.analog-stereo",
			Type:        DeviceTypeSpeaker,
			IsDefault:   true,
			Available:   true,
			Volume:      0.53,
			Muted:       true,
		},
		{
			ID:          "bluez_output.3C_B0_ED_3A_2C_42.1",
			Name:        "JBL Tune 520BT",
			Description: "bluez_output.3C_B0_ED_3A_2C_42.1",
			Type:        DeviceTypeBluetooth,
			Available:   true,
			Volume:      1.0,
			MACAddress:  "3C:B0:ED:3A:2C:42",
		},
		{
			ID:          "alsa_output.usb-Generic_Headset-00.analog-stereo",
			Name:        "Generic Headset Analog Stereo",
			Description: "alsa_output.usb-Generic_Headset-00.analog-stereo",
			Type:        DeviceTypeHeadphone,
			Available:   true,
			Volume:      0.70,
		},
	}

	if len(devices) != len(want) {
		t.Fatalf("built %d devices, want %d", len(devices), len(want))
	}
	for i, w := range want {
		if devices[i] != w {
			t.Errorf("device %d =\n\t%+v\nwant\n\t%+v", i, devices[i], w)
		}
	}
}

func TestDevicesFromEmptyList(t *testing.T) {
	if got := devicesFrom(nil, ""); len(got) != 0 {
		t.Errorf("devicesFrom(nil) = %v, want nothing", got)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		sink pactlSink
		want DeviceType
	}{
		{
			name: "bluez5 api",
			sink: pactlSink{Properties: map[string]string{"device.api": "bluez5"}},
			want: DeviceTypeBluetooth,
		},
		{
			name: "bluez5 address only",
			sink: pactlSink{Properties: map[string]string{"api.bluez5.address": "3C:B0:ED:3A:2C:42"}},
			want: DeviceTypeBluetooth,
		},
		{
			name: "pulseaudio module mac",
			sink: pactlSink{Properties: map[string]string{"bluetooth.device.mac": "AA:BB:CC:DD:EE:FF"}},
			want: DeviceTypeBluetooth,
		},
		{
			name: "bluez node name",
			sink: pactlSink{Properties: map[string]string{"node.name": "bluez_output.3C_B0_ED_3A_2C_42.1"}},
			want: DeviceTypeBluetooth,
		},
		{
			name: "bluez sink name with no properties",
			sink: pactlSink{Name: "bluez_output.3C_B0_ED_3A_2C_42.1"},
			want: DeviceTypeBluetooth,
		},
		{
			// A USB headset is a headphone first, because the priority list is
			// written in terms of what you put on your head.
			name: "headset form factor beats the usb bus",
			sink: pactlSink{Properties: map[string]string{"device.form.factor": "headset", "device.bus": "usb"}},
			want: DeviceTypeHeadphone,
		},
		{
			name: "headphone form factor",
			sink: pactlSink{Properties: map[string]string{"device.form.factor": "headphone"}},
			want: DeviceTypeHeadphone,
		},
		{
			name: "usb bus",
			sink: pactlSink{
				Description: "Razer Barracuda X Analog Stereo",
				Properties:  map[string]string{"device.bus": "usb"},
			},
			want: DeviceTypeUSB,
		},
		{
			name: "speaker form factor is not a headphone",
			sink: pactlSink{
				Description: "Built-in Audio",
				Properties:  map[string]string{"device.form.factor": "speaker"},
			},
			want: DeviceTypeSpeaker,
		},
		{
			name: "hdmi in the description",
			sink: pactlSink{
				Description: "GA104 Digital Stereo (HDMI)",
				Properties:  map[string]string{"device.bus": "pci"},
			},
			want: DeviceTypeHDMI,
		},
		{
			name: "displayport in the sink name",
			sink: pactlSink{Name: "alsa_output.pci-0000_01_00.1.displayport-stereo"},
			want: DeviceTypeHDMI,
		},
		{
			// What a plain PulseAudio server leaves us: no properties at all.
			name: "bluetooth by name alone",
			sink: pactlSink{Description: "JBL Tune 520BT Bluetooth"},
			want: DeviceTypeBluetooth,
		},
		{
			name: "usb by name alone",
			sink: pactlSink{Name: "alsa_output.usb-Razer-01.analog-stereo"},
			want: DeviceTypeUSB,
		},
		{
			name: "headphone by name alone",
			sink: pactlSink{Description: "Front Headphone"},
			want: DeviceTypeHeadphone,
		},
		{
			name: "anything else is a speaker",
			sink: pactlSink{Description: "Ryzen HD Audio Controller Analog Stereo"},
			want: DeviceTypeSpeaker,
		},
		{
			name: "nothing at all is a speaker",
			sink: pactlSink{},
			want: DeviceTypeSpeaker,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.sink); got != c.want {
				t.Errorf("classify(%+v) = %v, want %v", c.sink, got, c.want)
			}
		})
	}
}

func TestMACAddress(t *testing.T) {
	cases := []struct {
		name string
		sink pactlSink
		want string
	}{
		{
			name: "bluez5 address wins",
			sink: pactlSink{
				Name: "bluez_output.AA_BB_CC_DD_EE_FF.1",
				Properties: map[string]string{
					"api.bluez5.address":   "3C:B0:ED:3A:2C:42",
					"bluetooth.device.mac": "11:22:33:44:55:66",
				},
			},
			want: "3C:B0:ED:3A:2C:42",
		},
		{
			name: "pulseaudio module property is next",
			sink: pactlSink{Properties: map[string]string{"bluetooth.device.mac": "11:22:33:44:55:66"}},
			want: "11:22:33:44:55:66",
		},
		{
			name: "node name is the last resort",
			sink: pactlSink{Properties: map[string]string{"node.name": "bluez_output.3C_B0_ED_3A_2C_42.1"}},
			want: "3C:B0:ED:3A:2C:42",
		},
		{
			name: "sink name when there are no properties",
			sink: pactlSink{Name: "bluez_output.3C_B0_ED_3A_2C_42.1"},
			want: "3C:B0:ED:3A:2C:42",
		},
		{
			name: "nothing to go on",
			sink: pactlSink{Name: "alsa_output.pci-0000_0e_00.6.analog-stereo"},
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := macAddress(c.sink); got != c.want {
				t.Errorf("macAddress(%+v) = %q, want %q", c.sink, got, c.want)
			}
		})
	}
}

func TestMacFromNodeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"bluez_output.3C_B0_ED_3A_2C_42.1", "3C:B0:ED:3A:2C:42"},
		{"bluez_output.AA_BB_CC_DD_EE_FF.a2dp-sink", "AA:BB:CC:DD:EE:FF"},
		{"alsa_output.pci-0000_0e_00.6.analog-stereo", ""},
		{"bluez_output", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := macFromNodeName(c.in); got != c.want {
			t.Errorf("macFromNodeName(%q) = %q, want %q", c.in, got, c.want)
		}
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

func TestChannelVolume(t *testing.T) {
	cases := []struct {
		name string
		vol  map[string]pactlSinkVolume
		want float64
	}{
		{
			name: "stereo pair",
			vol: map[string]pactlSinkVolume{
				"front-left":  {ValuePercent: "45%"},
				"front-right": {ValuePercent: "45%"},
			},
			want: 0.45,
		},
		{
			// Sorting the names is what makes this answer the same every time.
			name: "channels that disagree pick the same one twice",
			vol: map[string]pactlSinkVolume{
				"front-left":  {ValuePercent: "30%"},
				"front-right": {ValuePercent: "80%"},
			},
			want: 0.30,
		},
		{
			name: "mono",
			vol:  map[string]pactlSinkVolume{"mono": {ValuePercent: "100%"}},
			want: 1.0,
		},
		{
			name: "no channels reported",
			vol:  nil,
			want: 1.0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for i := 0; i < 8; i++ {
				if got := channelVolume(c.vol); got != c.want {
					t.Fatalf("channelVolume(%v) = %v, want %v", c.vol, got, c.want)
				}
			}
		})
	}
}

func TestServerName(t *testing.T) {
	const pipewireInfo = `Server String: /run/user/1000/pulse/native
Library Protocol Version: 35
Is Local: yes
Host Name: fedora
Server Name: PulseAudio (on PipeWire 1.6.8)
Server Version: 15.0.0
Default Sink: alsa_output.pci-0000_0e_00.6.analog-stereo`

	const pulseInfo = `Server String: /run/user/1000/pulse/native
Host Name: fedora
Server Name: pulseaudio
Server Version: 16.1`

	cases := []struct {
		name string
		info string
		want string
	}{
		// pipewire-pulse names both servers on one line, so PipeWire has to
		// win the match or every PipeWire machine reports as PulseAudio.
		{"pipewire-pulse", pipewireInfo, "pipewire"},
		{"pulseaudio proper", pulseInfo, "pulseaudio"},
		{"no server name line", "Host Name: fedora\n", "pulseaudio"},
		{"nothing at all", "", "pulseaudio"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serverName(c.info); got != c.want {
				t.Errorf("serverName(%q) = %q, want %q", c.info, got, c.want)
			}
		})
	}
}

func TestDetectRejectsUnknownBackend(t *testing.T) {
	// The accepted values are not checked here, since every one of them ends
	// up asking a real pactl whether a server is listening.
	if _, err := Detect("jack"); err == nil {
		t.Error("Detect should reject a backend name it does not know")
	}
}
