package cli

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
)

var started = time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

// fixture has three devices with a Bluetooth default, one unavailable and
// muted, and a short log.
func fixture() ipc.Snapshot {
	return ipc.Snapshot{
		Devices: []audio.Device{
			{
				ID:          "alsa_output.pci-0000_00_1f.3.analog-stereo",
				Name:        "Built-in Audio Analog Stereo",
				Description: "alsa_output.pci-0000_00_1f.3.analog-stereo",
				Type:        audio.DeviceTypeSpeaker,
				Available:   true,
				Volume:      0.45,
			},
			{
				ID:          "bluez_output.3C_B0_ED_3A_2C_42.1",
				Name:        "JBL Tune 520BT",
				Description: "bluez_output.3C_B0_ED_3A_2C_42.1",
				Type:        audio.DeviceTypeBluetooth,
				IsDefault:   true,
				Available:   true,
				Volume:      0.8,
				MACAddress:  "3C:B0:ED:3A:2C:42",
			},
			{
				ID:          "alsa_output.usb-Razer_Barracuda_X",
				Name:        "Razer Barracuda X",
				Description: "alsa_output.usb-Razer_Barracuda_X",
				Type:        audio.DeviceTypeUSB,
				Volume:      1.0,
				Muted:       true,
			},
		},
		Status: ipc.StatusData{
			Backend:    "pipewire",
			ConfigPath: "/home/u/.config/poweraudio/config.toml",
			StartedAt:  started,
		},
		Events: []ipc.EventLog{
			{Time: started, Level: ipc.LevelInfo, Message: "using pipewire backend"},
			{Time: started.Add(time.Minute), Level: ipc.LevelInfo, Message: "bluetooth connected: JBL Tune 520BT"},
			{Time: started.Add(90 * time.Second), Level: ipc.LevelWarn, Message: "waiting for the audio sink of JBL Tune 520BT"},
		},
		Config: config.DefaultConfig(),
	}
}

// fakeClient hands out canned snapshots and records every request.
type fakeClient struct {
	snaps  []ipc.Snapshot
	nth    int
	err    error
	stream chan ipc.Snapshot
	calls  []string
}

func newFake(snaps ...ipc.Snapshot) *fakeClient {
	if len(snaps) == 0 {
		snaps = []ipc.Snapshot{fixture()}
	}
	return &fakeClient{snaps: snaps}
}

// downFake fails every call the way an unanswered socket dial does.
func downFake() *fakeClient {
	return &fakeClient{err: &net.OpError{
		Op:  "dial",
		Net: "unix",
		Err: syscall.ENOENT,
	}}
}

func (f *fakeClient) record(format string, a ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
}

// Snapshot repeats the last canned snapshot once the list runs out.
func (f *fakeClient) Snapshot() (*ipc.Snapshot, error) {
	f.record("snapshot")
	if f.err != nil {
		return nil, f.err
	}
	i := min(f.nth, len(f.snaps)-1)
	f.nth++
	snap := f.snaps[i]
	return &snap, nil
}

func (f *fakeClient) Subscribe(_ context.Context) (<-chan ipc.Snapshot, error) {
	f.record("subscribe")
	if f.err != nil {
		return nil, f.err
	}
	return f.stream, nil
}

func (f *fakeClient) SetDefault(deviceID string, notify bool) error {
	if notify {
		f.record("set_default %s notify", deviceID)
	} else {
		f.record("set_default %s", deviceID)
	}
	return f.err
}

func (f *fakeClient) SetVolume(deviceID string, percent int) error {
	f.record("set_volume %s %d", deviceID, percent)
	return f.err
}

func (f *fakeClient) ToggleMute(deviceID string) error {
	f.record("toggle_mute %s", deviceID)
	return f.err
}

func (f *fakeClient) ReloadConfig() error {
	f.record("reload_config")
	return f.err
}
