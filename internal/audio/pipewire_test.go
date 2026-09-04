package audio

import "testing"

// A trimmed capture of `wpctl status`, keeping the box-drawing tree exactly as
// wpctl draws it. The default marker never starts the line, and the Sources
// section carries a marker of its own that must not be mistaken for a sink.
const wpctlStatusSample = "PipeWire 'pipewire-0' [1.6.8, user@host, cookie:1]\n" +
	" └─ Clients:\n" +
	"        33. pipewire                            [1.6.8, user@host, pid:1]\n" +
	"\n" +
	"Audio\n" +
	" ├─ Devices:\n" +
	" │      52. GA104 High Definition Audio Controller [alsa]\n" +
	" │  \n" +
	" ├─ Sinks:\n" +
	" │      44. Razer Barracuda X Analog Stereo     [vol: 0.40]\n" +
	" │      45. KREO REC Analog Stereo              [vol: 0.35 MUTED]\n" +
	" │  *   67. Ryzen HD Audio Controller Analog Stereo [vol: 0.45]\n" +
	" │  \n" +
	" ├─ Sources:\n" +
	" │  *   80. KREO REC Analog Stereo              [vol: 0.88]\n" +
	" │  \n" +
	" └─ Streams:\n" +
	"        76. Brave\n"

func TestParseWpctlStatus(t *testing.T) {
	defaultID, levels := parseWpctlStatus(wpctlStatusSample)

	if defaultID != "67" {
		t.Errorf("default sink = %q, want %q", defaultID, "67")
	}
	if len(levels) != 3 {
		t.Fatalf("parsed %d sinks, want 3: %v", len(levels), levels)
	}

	want := map[string]sinkLevel{
		"44": {volume: 0.40},
		"45": {volume: 0.35, muted: true},
		"67": {volume: 0.45},
	}
	for id, w := range want {
		got, ok := levels[id]
		if !ok {
			t.Errorf("sink %s missing from %v", id, levels)
			continue
		}
		if got != w {
			t.Errorf("sink %s = %+v, want %+v", id, got, w)
		}
	}
	if _, ok := levels["80"]; ok {
		t.Error("a source was counted as a sink")
	}
	if _, ok := levels["52"]; ok {
		t.Error("a device was counted as a sink")
	}
}

func TestParseWpctlStatusNoDefault(t *testing.T) {
	out := " ├─ Sinks:\n │      44. Speakers [vol: 0.40]\n"
	defaultID, levels := parseWpctlStatus(out)
	if defaultID != "" {
		t.Errorf("default = %q, want empty", defaultID)
	}
	if len(levels) != 1 {
		t.Errorf("parsed %d sinks, want 1", len(levels))
	}
}

func TestParseVolumeTag(t *testing.T) {
	cases := []struct {
		line  string
		vol   float64
		muted bool
		ok    bool
	}{
		{"44. Speakers [vol: 0.40]", 0.40, false, true},
		{"45. Headset [vol: 1.00 MUTED]", 1.00, true, true},
		{"46. Loud [vol: 1.53]", 1.53, false, true},
		{"47. No volume reported", 0, false, false},
		{"48. Broken [vol: loud]", 0, false, false},
		{"49. Unclosed [vol: 0.30", 0, false, false},
	}
	for _, c := range cases {
		vol, muted, ok := parseVolumeTag(c.line)
		if ok != c.ok || vol != c.vol || muted != c.muted {
			t.Errorf("parseVolumeTag(%q) = (%v, %v, %v), want (%v, %v, %v)",
				c.line, vol, muted, ok, c.vol, c.muted, c.ok)
		}
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

func TestParsePactlEvent(t *testing.T) {
	cases := []struct {
		line     string
		wantType EventType
		wantID   string
		wantOK   bool
	}{
		{"Event 'new' on sink #43", EventSinkAdded, "43", true},
		{"Event 'remove' on sink #43", EventSinkRemoved, "43", true},
		{"Event 'change' on sink #73", EventSinkChanged, "73", true},
		{"Event 'change' on server #0", EventDefaultChanged, "0", true},
		// A stream, not an output. These arrive whenever an application
		// starts or stops playing and used to look like sink changes.
		{"Event 'change' on sink-input #52", 0, "", false},
		{"Event 'new' on client #4779", 0, "", false},
		{"Event 'change' on card #57", 0, "", false},
		{"Got SIGINT, exiting.", 0, "", false},
		{"", 0, "", false},
	}
	for _, c := range cases {
		ev, ok := parsePactlEvent(c.line)
		if ok != c.wantOK {
			t.Errorf("parsePactlEvent(%q) ok = %v, want %v", c.line, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if ev.Type != c.wantType || ev.DeviceID != c.wantID {
			t.Errorf("parsePactlEvent(%q) = (%v, %q), want (%v, %q)",
				c.line, ev.Type, ev.DeviceID, c.wantType, c.wantID)
		}
	}
}

func TestClassifyDevice(t *testing.T) {
	cases := []struct {
		props map[string]any
		want  DeviceType
	}{
		{map[string]any{"device.api": "bluez5"}, DeviceTypeBluetooth},
		{map[string]any{"node.name": "bluez_output.AA_BB.1"}, DeviceTypeBluetooth},
		{map[string]any{"device.bus": "usb"}, DeviceTypeUSB},
		{map[string]any{"node.description": "GA104 Digital Stereo (HDMI)"}, DeviceTypeHDMI},
		{map[string]any{"node.description": "Front Headphone"}, DeviceTypeHeadphone},
		{map[string]any{"node.description": "Ryzen HD Audio"}, DeviceTypeSpeaker},
		{map[string]any{}, DeviceTypeSpeaker},
	}
	for _, c := range cases {
		if got := classifyDevice(c.props); got != c.want {
			t.Errorf("classifyDevice(%v) = %v, want %v", c.props, got, c.want)
		}
	}
}
