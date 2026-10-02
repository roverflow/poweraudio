package bluetooth

import "testing"

func TestIsAudio(t *testing.T) {
	const (
		a2dpSink = "0000110b-0000-1000-8000-00805f9b34fb"
		hid      = "00001124-0000-1000-8000-00805f9b34fb"
		gatt     = "00001801-0000-1000-8000-00805f9b34fb"
		pacs     = "00001850-0000-1000-8000-00805F9B34FB"
	)
	cases := []struct {
		name  string
		uuids []string
		icon  string
		want  bool
	}{
		{"headphones", []string{gatt, a2dpSink}, "audio-headset", true},
		{"LE Audio earbuds, upper case uuid", []string{pacs}, "", true},
		{"mouse", []string{gatt, hid}, "input-mouse", false},
		{"keyboard with no icon", []string{hid}, "", false},
		{"profiles not resolved yet, audio icon", nil, "audio-headphones", true},
		{"nothing known", nil, "", true},
	}
	for _, c := range cases {
		if got := isAudio(c.uuids, c.icon); got != c.want {
			t.Errorf("%s: isAudio = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMACFromPath(t *testing.T) {
	if got := macFromPath("/org/bluez/hci0/dev_3C_B0_ED_3A_2C_42"); got != "3C:B0:ED:3A:2C:42" {
		t.Errorf("macFromPath = %q", got)
	}
	if got := macFromPath("/org/bluez/hci0"); got != "" {
		t.Errorf("adapter path gave a MAC: %q", got)
	}
}
