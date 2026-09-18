package cli

import (
	"strings"
	"testing"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/ipc"
)

func TestFindDevice(t *testing.T) {
	devices := []audio.Device{
		{ID: "sink.jbl", Name: "JBL", Description: "bluez_output.jbl", MACAddress: "3C:B0:ED:3A:2C:42"},
		{ID: "sink.jbl.tune", Name: "JBL Tune 520BT", Description: "bluez_output.jbl.tune"},
		{ID: "sink.hdmi", Name: "GA104 Digital Stereo (HDMI)", Description: "alsa_output.hdmi"},
	}

	cases := []struct {
		name    string
		query   string
		wantID  string
		wantErr string
	}{
		{name: "by id", query: "sink.hdmi", wantID: "sink.hdmi"},
		{name: "by name", query: "GA104 Digital Stereo (HDMI)", wantID: "sink.hdmi"},
		{name: "ignores case", query: "ga104 digital stereo (hdmi)", wantID: "sink.hdmi"},
		{name: "by description", query: "alsa_output.hdmi", wantID: "sink.hdmi"},
		{name: "by MAC address", query: "3c:b0:ed:3a:2c:42", wantID: "sink.jbl"},
		{name: "unique substring", query: "tune", wantID: "sink.jbl.tune"},
		{name: "substring of the MAC address", query: "3c:b0", wantID: "sink.jbl"},
		// "jbl" is a substring of two devices, so without the exact name
		// rule the shorter device would be unreachable.
		{name: "exact beats substring", query: "jbl", wantID: "sink.jbl"},
		{name: "surrounding space", query: "  tune  ", wantID: "sink.jbl.tune"},
		{
			name:    "ambiguous",
			query:   "bluez_output",
			wantErr: `"bluez_output" matches more than one device: JBL, JBL Tune 520BT`,
		},
		{
			name:    "no match",
			query:   "sennheiser",
			wantErr: `no device matches "sennheiser" (devices: JBL, JBL Tune 520BT, GA104 Digital Stereo (HDMI))`,
		},
		{name: "empty query", query: "   ", wantErr: "empty device query"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dev, err := findDevice(devices, c.query)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("findDevice(%q) = %+v, want an error", c.query, dev)
				}
				if err.Error() != c.wantErr {
					t.Errorf("error = %q, want %q", err.Error(), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("findDevice(%q): %v", c.query, err)
			}
			if dev.ID != c.wantID {
				t.Errorf("findDevice(%q) = %q, want %q", c.query, dev.ID, c.wantID)
			}
		})
	}
}

func TestFindDeviceWithNoDevices(t *testing.T) {
	if _, err := findDevice(nil, "jbl"); err == nil {
		t.Fatal("findDevice on an empty list succeeded, want an error")
	} else if !strings.Contains(err.Error(), "no output devices") {
		t.Errorf("error = %q, want it to mention that there are no devices", err)
	}
}

// sink is a device in whatever state a next test needs.
func sink(id string, isDefault, available bool) audio.Device {
	return audio.Device{ID: id, Name: id, IsDefault: isDefault, Available: available}
}

func TestNextDevice(t *testing.T) {
	cases := []struct {
		name    string
		devices []audio.Device
		wantID  string
		wantErr string
	}{
		{
			name:    "the one after the default",
			devices: []audio.Device{sink("a", true, true), sink("b", false, true), sink("c", false, true)},
			wantID:  "b",
		},
		{
			name:    "wraps past the end",
			devices: []audio.Device{sink("a", false, true), sink("b", false, true), sink("c", true, true)},
			wantID:  "a",
		},
		{
			name:    "skips an unavailable device",
			devices: []audio.Device{sink("a", true, true), sink("b", false, false), sink("c", false, true)},
			wantID:  "c",
		},
		{
			name:    "skips an unavailable device while wrapping",
			devices: []audio.Device{sink("a", false, false), sink("b", false, true), sink("c", true, true)},
			wantID:  "b",
		},
		{
			name:    "starts at the top when nothing is default",
			devices: []audio.Device{sink("a", false, true), sink("b", false, true)},
			wantID:  "a",
		},
		{
			name:    "comes back to the only available device",
			devices: []audio.Device{sink("a", true, true), sink("b", false, false)},
			wantID:  "a",
		},
		{
			name:    "nothing available",
			devices: []audio.Device{sink("a", true, false), sink("b", false, false)},
			wantErr: "no available device to switch to",
		},
		{
			name:    "no devices at all",
			devices: nil,
			wantErr: "no output devices",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dev, err := nextDevice(c.devices)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("nextDevice = %+v, want an error", dev)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("nextDevice: %v", err)
			}
			if dev.ID != c.wantID {
				t.Errorf("nextDevice = %q, want %q", dev.ID, c.wantID)
			}
		})
	}
}

func TestVolumeSpec(t *testing.T) {
	cases := []struct {
		name    string
		arg     string
		current int
		want    int
		wantErr bool
	}{
		{name: "absolute", arg: "50", current: 20, want: 50},
		{name: "absolute with a percent sign", arg: "50%", current: 20, want: 50},
		{name: "relative up", arg: "+5", current: 20, want: 25},
		{name: "relative down", arg: "-5", current: 20, want: 15},
		{name: "relative down past zero", arg: "-40", current: 20, want: 0},
		{name: "relative up past the ceiling", arg: "+60", current: 120, want: 150},
		{name: "absolute above the ceiling", arg: "400", current: 20, want: 150},
		{name: "relative zero changes nothing", arg: "-0", current: 20, want: 20},
		{name: "zero", arg: "0", current: 20, want: 0},
		{name: "the ceiling itself", arg: "150", current: 20, want: 150},
		{name: "not a number", arg: "loud", wantErr: true},
		{name: "empty", arg: "", wantErr: true},
		{name: "only a percent sign", arg: "%", wantErr: true},
		{name: "trailing rubbish", arg: "50db", wantErr: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, err := parseVolumeSpec(c.arg)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseVolumeSpec(%q) = %+v, want an error", c.arg, spec)
				}
				if !isUsageError(err) {
					t.Errorf("error %v is not a usage error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVolumeSpec(%q): %v", c.arg, err)
			}
			if got := spec.apply(c.current); got != c.want {
				t.Errorf("%q applied to %d = %d, want %d", c.arg, c.current, got, c.want)
			}
		})
	}
}

func TestVolumePercent(t *testing.T) {
	cases := []struct {
		volume float64
		want   int
	}{
		{0, 0},
		{0.45, 45},
		{0.455, 46},
		{1, 100},
		{1.5, 150},
	}

	for _, c := range cases {
		if got := volumePercent(c.volume); got != c.want {
			t.Errorf("volumePercent(%v) = %d, want %d", c.volume, got, c.want)
		}
	}
}

func TestTargetDevice(t *testing.T) {
	snap := fixture()

	dev, err := targetDevice(&snap, "")
	if err != nil {
		t.Fatalf("targetDevice with no query: %v", err)
	}
	if dev.Name != "JBL Tune 520BT" {
		t.Errorf("targetDevice with no query = %q, want the default device", dev.Name)
	}

	dev, err = targetDevice(&snap, "razer")
	if err != nil {
		t.Fatalf("targetDevice with a query: %v", err)
	}
	if dev.Name != "Razer Barracuda X" {
		t.Errorf("targetDevice = %q, want the device the query names", dev.Name)
	}

	// Nothing is default while the daemon is between switches, and a command
	// with no query has nothing to act on.
	headless := ipc.Snapshot{Devices: []audio.Device{{ID: "a", Name: "a"}}}
	if _, err := targetDevice(&headless, ""); err == nil {
		t.Error("targetDevice with no default succeeded, want an error")
	}
}
