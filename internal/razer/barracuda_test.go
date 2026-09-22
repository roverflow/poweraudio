package razer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseReportTracksThePowerSwitch(t *testing.T) {
	// Three pushes from the dongle on this machine. The first two are one
	// sitting: off, then on. The third is an earlier power-on. Byte 13 is
	// the only one that follows the switch. The bytes around it move on
	// every push.
	off := "020e504911d556dd16000400040000324901c0a43000000300118d1b32504901c0830c0000030011e91900000000000000000000000000000000000000000000"
	on := "020e504911d7c10017000400040100324901c0a43000000300118d1b32504901c0830c0000030011e91900000000000000000000000000000000000000000000"
	onEarlier := "020e504911d23e8014000400040100324901c0a43000000300118d1b32504901c0830c0000030011e91900000000000000000000000000000000000000000000"

	cases := []struct {
		name string
		hex  string
		on   bool
	}{
		{"earcups off", off, false},
		{"earcups on", on, true},
		{"earcups on, earlier push", onEarlier, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseReport(mustHex(t, c.hex))
			if !ok {
				t.Fatal("report was not recognised")
			}
			if got != c.on {
				t.Errorf("on = %v, want %v", got, c.on)
			}
		})
	}
}

func TestParseReportIgnoresAnythingElse(t *testing.T) {
	on := mustHex(t, "020e504911d7c10017000400040100324901c0a43000000300118d1b32504901c0830c0000030011e91900000000000000000000000000000000000000000000")
	short := append([]byte(nil), on[:connectedByte]...)
	volume := []byte{0x01, 0xe9, 0x00, 0x00, 0x00}
	unknown := append([]byte(nil), on...)
	unknown[connectedByte] = 0x02

	for _, b := range [][]byte{nil, short, volume, unknown} {
		if _, ok := ParseReport(b); ok {
			t.Errorf("accepted %x", b)
		}
	}
}

func TestFindHidrawName(t *testing.T) {
	root := t.TempDir()
	writeUevent(t, root, "hidraw1", "HID_ID=0003:0000046D:0000C53F\n")
	writeUevent(t, root, "hidraw4", "DRIVER=hid-generic\nHID_ID=0003:00001532:0000054E\nHID_NAME=Razer Barracuda X\n")

	got, err := findHidrawName(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hidraw4" {
		t.Errorf("found %s, want hidraw4", got)
	}

	if _, err := findHidrawName(t.TempDir()); err == nil {
		t.Fatal("an empty directory reported a dongle")
	}
}

func writeUevent(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name, "device")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	if len(s)%2 != 0 {
		t.Fatalf("odd hex length %d", len(s))
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		out[i] = hexByte(t, s[i*2:i*2+2])
	}
	return out
}

func hexByte(t *testing.T, s string) byte {
	t.Helper()
	var n byte
	for _, c := range s {
		n <<= 4
		switch {
		case c >= '0' && c <= '9':
			n |= byte(c - '0')
		case c >= 'a' && c <= 'f':
			n |= byte(c-'a') + 10
		default:
			t.Fatalf("bad hex %q", s)
		}
	}
	return n
}
