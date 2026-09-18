package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/ipc"
)

func TestRenderList(t *testing.T) {
	snap := fixture()
	want := `   Built-in Audio Analog Stereo  Speaker    45%    alsa_output.pci-0000_00_1f.3.analog-stereo
*  JBL Tune 520BT                Bluetooth  80%    bluez_output.3C_B0_ED_3A_2C_42.1
   Razer Barracuda X             USB        muted  alsa_output.usb-Razer_Barracuda_X
`

	if got := renderList(&snap); got != want {
		t.Errorf("renderList =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderListWithNoDevices(t *testing.T) {
	snap := ipc.Snapshot{}
	if got := renderList(&snap); got != "no output devices\n" {
		t.Errorf("renderList = %q, want %q", got, "no output devices\n")
	}
}

func TestRenderStatus(t *testing.T) {
	snap := fixture()
	want := `default  JBL Tune 520BT  80%
backend  pipewire
config   /home/u/.config/poweraudio/config.toml
uptime   2h 3m

events
Sep 18 09:00:00  info  using pipewire backend
Sep 18 09:01:00  info  bluetooth connected: JBL Tune 520BT
Sep 18 09:01:30  warn  waiting for the audio sink of JBL Tune 520BT
`

	if got := renderStatus(&snap, started.Add(2*time.Hour+3*time.Minute)); got != want {
		t.Errorf("renderStatus =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderStatusWithoutADefaultOrEvents(t *testing.T) {
	snap := fixture()
	snap.Devices[1].IsDefault = false
	snap.Events = nil
	snap.Status.StartedAt = time.Time{}

	got := renderStatus(&snap, started)
	if !strings.Contains(got, "default  none") {
		t.Errorf("renderStatus = %q, want it to report no default device", got)
	}
	if !strings.Contains(got, "uptime   unknown") {
		t.Errorf("renderStatus = %q, want an unknown uptime for a daemon with no start time", got)
	}
	if strings.Contains(got, "events") {
		t.Errorf("renderStatus = %q, want no events heading when there are none", got)
	}
}

func TestRenderStatusShowsTheLastTenEvents(t *testing.T) {
	snap := fixture()
	snap.Events = nil
	for i := 0; i < 25; i++ {
		snap.Events = append(snap.Events, ipc.EventLog{
			Time:    started.Add(time.Duration(i) * time.Second),
			Level:   ipc.LevelInfo,
			Message: "event " + string(rune('a'+i)),
		})
	}

	got := renderStatus(&snap, started)
	if strings.Contains(got, "event o") {
		t.Errorf("renderStatus = %q, want the fifteenth event dropped", got)
	}
	if !strings.Contains(got, "event p") || !strings.Contains(got, "event y") {
		t.Errorf("renderStatus = %q, want the last ten events", got)
	}
	if lines := strings.Count(got, "\nSep 18"); lines != statusEvents {
		t.Errorf("renderStatus printed %d event lines, want %d", lines, statusEvents)
	}
}

func TestWatchLine(t *testing.T) {
	loud := fixture()
	muted := fixture()
	muted.Devices[1].Muted = true
	headless := fixture()
	headless.Devices[1].IsDefault = false

	cases := []struct {
		name string
		snap ipc.Snapshot
		want string
	}{
		{name: "default with a level", snap: loud, want: "JBL Tune 520BT  80%"},
		{name: "muted default", snap: muted, want: "JBL Tune 520BT  muted"},
		{name: "no default", snap: headless, want: "none"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := watchLine(c.snap); got != c.want {
				t.Errorf("watchLine = %q, want %q", got, c.want)
			}
		})
	}
}

func TestVolumeLabel(t *testing.T) {
	cases := []struct {
		dev  audio.Device
		want string
	}{
		{audio.Device{Volume: 0.45}, "45%"},
		{audio.Device{Volume: 0}, "0%"},
		{audio.Device{Volume: 1.5}, "150%"},
		{audio.Device{Volume: 0.45, Muted: true}, "muted"},
	}

	for _, c := range cases {
		if got := volumeLabel(c.dev); got != c.want {
			t.Errorf("volumeLabel(%+v) = %q, want %q", c.dev, got, c.want)
		}
	}
}

func TestUptime(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "seconds", d: 45 * time.Second, want: "45s"},
		{name: "minutes", d: 12*time.Minute + 5*time.Second, want: "12m 5s"},
		{name: "hours", d: 2*time.Hour + 3*time.Minute + 59*time.Second, want: "2h 3m"},
		{name: "days", d: 50 * time.Hour, want: "2d 2h"},
		{name: "just started", d: 0, want: "0s"},
		{name: "clock stepped backwards", d: -time.Minute, want: "0s"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := uptime(started, started.Add(c.d)); got != c.want {
				t.Errorf("uptime(%v) = %q, want %q", c.d, got, c.want)
			}
		})
	}
}
