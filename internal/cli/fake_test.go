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

// started is the daemon start time in the fixture snapshot. Tests that render
// an uptime pass an explicit "now" relative to it.
var started = time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

// fixture is the snapshot every test renders against: three devices with a
// Bluetooth default, one of them unavailable and muted, and a short log.
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

// fakeClient stands in for the daemon. It hands out canned snapshots, records
// every request and can fail the way a missing socket does.
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

// downFake fails every call the way dialling a socket with nothing behind it
// does, which is the case every command has to report the same way.
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

// Snapshot walks the canned list, repeating the last one once it runs out, so
// a command that reads state back sees the second snapshot it was given.
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

func (f *fakeClient) SetDefault(deviceID string) error {
	f.record("set_default %s", deviceID)
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
