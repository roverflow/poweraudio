package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/bluetooth"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
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

// The pactl event carries a pulse index and the sink list is keyed by name, so
// the log has to work the name out by diffing the list.
func TestSinkEventsLogDeviceNames(t *testing.T) {
	backend := &stubBackend{devices: []audio.Device{
		{ID: "alsa_output.pci-0000_00_1f.3.analog-stereo", Name: "Built-in Audio", Available: true},
	}}
	d := New(config.DefaultConfig(), backend, filepath.Join(t.TempDir(), "config.toml"))
	ctx := context.Background()
	d.refreshDevices(ctx)

	backend.add(audio.Device{
		ID:        "bluez_output.3C_B0_ED_3A_2C_42.1",
		Name:      "JBL Tune 520BT",
		Type:      audio.DeviceTypeBluetooth,
		Available: true,
	})
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkAdded, DeviceID: "1648"})

	last := lastEvent(t, d)
	if !strings.Contains(last.Message, "JBL Tune 520BT") {
		t.Errorf("sink added logged %q, want the name of the sink that appeared", last.Message)
	}
	if strings.Contains(last.Message, "1648") {
		t.Errorf("sink added logged the pulse index: %q", last.Message)
	}
	if last.Level != ipc.LevelDebug {
		t.Errorf("sink added logged at %s, want debug", last.Level)
	}

	backend.remove("bluez_output.3C_B0_ED_3A_2C_42.1")
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkRemoved, DeviceID: "1648"})

	last = lastEvent(t, d)
	if !strings.Contains(last.Message, "JBL Tune 520BT") {
		t.Errorf("sink removed logged %q, want the name of the sink that vanished", last.Message)
	}

	// Nothing moved, so there is no name to report and the raw id is all the
	// daemon knows.
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkAdded, DeviceID: "1648"})
	if last := lastEvent(t, d); !strings.Contains(last.Message, "1648") {
		t.Errorf("sink added with no list change logged %q, want the raw id", last.Message)
	}
}

func TestRemovingTheDefaultFallsBack(t *testing.T) {
	backend := rankedSinks(t, "barracuda")
	d := rankedDaemon(t, backend)
	ctx := context.Background()
	d.refreshDevices(ctx)

	backend.remove("barracuda")
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkRemoved})

	if got := backend.defaultID(); got != "headset" {
		t.Errorf("default = %q, want the next ranked device that can play", got)
	}
}

func TestUnavailableDefaultFallsBack(t *testing.T) {
	backend := rankedSinks(t, "barracuda")
	d := rankedDaemon(t, backend)
	ctx := context.Background()
	d.refreshDevices(ctx)

	// A port going empty arrives as a sink change. The event loop collapses
	// the burst, then makes the same call.
	backend.setAvailable("barracuda", false)
	d.refreshSettled(ctx)

	if got := backend.defaultID(); got != "headset" {
		t.Errorf("default = %q, want the next ranked device that can play", got)
	}
}

func TestStartupLeavesAPortThatIsAlreadyDown(t *testing.T) {
	backend := rankedSinks(t, "barracuda")
	backend.setAvailable("barracuda", false)
	d := rankedDaemon(t, backend)

	d.refreshInitial(context.Background())

	if got := backend.defaultID(); got != "headset" {
		t.Errorf("default = %q, want the next ranked device that can play", got)
	}
}

func TestUnknownPortStaysTheDefault(t *testing.T) {
	// availability unknown is stored as Available. A refresh must not move it.
	backend := rankedSinks(t, "barracuda")
	d := rankedDaemon(t, backend)
	ctx := context.Background()
	d.refreshDevices(ctx)

	d.refreshSettled(ctx)

	if got := backend.defaultID(); got != "barracuda" {
		t.Errorf("default = %q, want the dongle left where it was", got)
	}
}

func TestRemovingAnotherSinkLeavesTheDefault(t *testing.T) {
	backend := rankedSinks(t, "ryzen")
	d := rankedDaemon(t, backend)
	ctx := context.Background()
	d.refreshDevices(ctx)

	backend.remove("barracuda")
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkRemoved})

	if got := backend.defaultID(); got != "ryzen" {
		t.Errorf("default = %q, want the device that was already playing", got)
	}
}

func TestFallbackAlreadyInPlaceIsQuiet(t *testing.T) {
	backend := rankedSinks(t, "barracuda")
	d := rankedDaemon(t, backend)
	ctx := context.Background()
	d.refreshDevices(ctx)

	// The sound server has already moved to the device the ranking would pick.
	backend.remove("barracuda")
	backend.setCurrent("headset")
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkRemoved})

	if got := backend.defaultID(); got != "headset" {
		t.Errorf("default = %q, want it left on the device the server chose", got)
	}
	for _, ev := range d.Snapshot().Events {
		if strings.Contains(ev.Message, "fallback to") {
			t.Errorf("announced a switch that had already happened: %q", ev.Message)
		}
	}
}

func TestEarcupsOffFallsBack(t *testing.T) {
	backend := barracudaSinks(t, "barracuda")
	d := rankedDaemon(t, backend)
	ctx := context.Background()
	d.refreshDevices(ctx)

	d.handleEarcups(ctx, false)

	if got := backend.defaultID(); got != "ryzen" {
		t.Errorf("default = %q, want the speakers after the earcups powered off", got)
	}
	for _, dev := range d.GetDevices() {
		if dev.ID == "barracuda" && dev.Available {
			t.Error("the powered-off headset is still available")
		}
	}
}

func TestEarcupsOnTakesTheOutput(t *testing.T) {
	backend := barracudaSinks(t, "ryzen")
	d := rankedDaemon(t, backend)
	ctx := context.Background()
	d.refreshDevices(ctx)

	d.handleEarcups(ctx, true)

	if got := backend.defaultID(); got != "barracuda" {
		t.Errorf("default = %q, want the headset after the earcups powered on", got)
	}
}

func TestEarcupsOnLeavesTheOutputWhenConnectIsNever(t *testing.T) {
	backend := barracudaSinks(t, "ryzen")
	d := rankedDaemon(t, backend)
	cfg := d.Config()
	cfg.Switching.OnConnect = "never"
	d.applyConfig(cfg)
	ctx := context.Background()
	d.refreshDevices(ctx)

	d.handleEarcups(ctx, true)

	if got := backend.defaultID(); got != "ryzen" {
		t.Errorf("default = %q, want it left alone when on_connect is never", got)
	}
}

func barracudaSinks(t *testing.T, current string) *stubBackend {
	t.Helper()
	return &stubBackend{
		current: current,
		devices: []audio.Device{
			{
				ID:        "barracuda",
				Name:      "Razer Barracuda X Analog Stereo",
				VendorID:  0x1532,
				ProductID: 0x054e,
				Available: true,
			},
			{ID: "ryzen", Name: "Ryzen HD Audio Controller", Available: true},
		},
	}
}

func rankedSinks(t *testing.T, current string) *stubBackend {
	t.Helper()
	return &stubBackend{
		current: current,
		devices: []audio.Device{
			{ID: "barracuda", Name: "Razer Barracuda X", Available: true},
			{ID: "headset", Name: "Headphones", Available: true},
			{ID: "ryzen", Name: "Ryzen HD Audio Controller", Available: true},
		},
	}
}

func rankedDaemon(t *testing.T, backend *stubBackend) *Daemon {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Notifications.Enabled = false
	cfg.Priority = []config.PriorityEntry{
		{Match: "Barracuda"},
		{Match: "Headphones"},
		{Match: "Ryzen"},
	}
	return New(cfg, backend, filepath.Join(t.TempDir(), "config.toml"))
}

// Headset A waiting for its sink used to be forgotten the moment unrelated
// headset B disconnected.
func TestDisconnectOfAnotherDeviceKeepsPending(t *testing.T) {
	backend := &stubBackend{
		devices: []audio.Device{{ID: "1", Name: "Speakers", Available: true}},
		current: "1",
	}
	d := New(config.DefaultConfig(), backend, filepath.Join(t.TempDir(), "config.toml"))
	ctx := context.Background()
	d.refreshDevices(ctx)

	waiting := &pendingBT{mac: "AA:BB:CC:DD:EE:FF", name: "Headset A", expiry: time.Now().Add(pendingTTL)}
	d.mu.Lock()
	d.pending = waiting
	d.mu.Unlock()

	d.handleBluetoothEvent(ctx, bluetooth.Event{
		MACAddress: "11:22:33:44:55:66",
		DeviceName: "Headset B",
	})
	if !d.hasPending() {
		t.Fatal("an unrelated disconnect dropped the device that was still waiting for its sink")
	}

	// The device being waited on is the one that cancels the wait.
	d.handleBluetoothEvent(ctx, bluetooth.Event{
		MACAddress: "aa:bb:cc:dd:ee:ff",
		DeviceName: "Headset A",
	})
	if d.hasPending() {
		t.Error("the pending device disconnected and the daemon is still waiting for it")
	}
}

// Editing the config by hand should take effect without a restart.
func TestConfigWatchReloadsRewrittenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := config.DefaultConfig()
	cfg.Switching.OnConnect = "always"
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	old := configPollInterval
	configPollInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		configPollInterval = old
	}()

	d := New(cfg, &stubBackend{}, path)
	go d.runEvents(ctx, nil, nil, nil)

	// The watch compares mtimes, so the rewrite has to land after the first
	// poll has read the original one.
	time.Sleep(50 * time.Millisecond)
	cfg.Switching.OnConnect = "never"
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if d.Config().Switching.OnConnect == "never" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("on_connect is still %q, want the rewritten file to have been reloaded",
		d.Config().Switching.OnConnect)
}

// A parse error leaves the daemon running on what it already had.
func TestConfigWatchKeepsRunningConfigOnParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := config.DefaultConfig()
	cfg.Switching.OnConnect = "priority"
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	d := New(cfg, &stubBackend{}, path)
	if err := os.WriteFile(path, []byte("this is not toml = = ="), 0o644); err != nil {
		t.Fatal(err)
	}

	d.checkConfigFile(time.Time{})

	if got := d.Config().Switching.OnConnect; got != "priority" {
		t.Errorf("on_connect = %q, want the running config to survive a broken file", got)
	}
	if last := lastEvent(t, d); last.Level != ipc.LevelError {
		t.Errorf("a broken config logged at %s, want error", last.Level)
	}
}

func TestLogLevelFiltersStderrNotTheRing(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.General.LogLevel = "warn"

	d := New(cfg, &stubBackend{}, filepath.Join(dir, "config.toml"))
	logPath := filepath.Join(dir, "poweraudio.log")
	d.setLogFile(logPath)
	defer d.closeLogFile()

	var out lockedBuffer
	log.SetOutput(&out)
	defer log.SetOutput(os.Stderr)

	d.debugf("sink added: Built-in Audio")
	d.infof("switched to JBL Tune 520BT")
	d.warnf("skipping switch: A is not ranked above B")

	stderr := out.String()
	if strings.Contains(stderr, "sink added") || strings.Contains(stderr, "switched to") {
		t.Errorf("log_level=warn still printed quieter levels: %q", stderr)
	}
	if !strings.Contains(stderr, "skipping switch") {
		t.Errorf("log_level=warn dropped a warning: %q", stderr)
	}

	// The status screen shows what the daemon did, whatever the level.
	events := d.Snapshot().Events
	if len(events) != 3 {
		t.Fatalf("the ring kept %d events, want all 3", len(events))
	}
	if events[0].Level != ipc.LevelDebug || events[2].Level != ipc.LevelWarn {
		t.Errorf("levels in the ring = %s, %s", events[0].Level, events[2].Level)
	}

	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading the log file: %v", err)
	}
	if !strings.Contains(string(written), "skipping switch") {
		t.Errorf("log file = %q, want the same line that went to stderr", written)
	}
	if strings.Contains(string(written), "sink added") {
		t.Errorf("log file took a line the level filter dropped: %q", written)
	}
}

// Saving is what makes a choice survive a restart, so a failed save is not a
// success no matter how the change went in memory.
func TestUpdatePrioritiesReportsSaveFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "config.toml")

	d := New(config.DefaultConfig(), &stubBackend{}, path)
	params, err := json.Marshal([]config.PriorityEntry{{Match: "JBL"}})
	if err != nil {
		t.Fatal(err)
	}

	resp := d.Handle(context.Background(), ipc.Request{
		Method: ipc.MethodUpdatePriorities,
		Params: params,
	})
	if resp.OK {
		t.Fatal("a save that never happened was reported as a success")
	}
	if !strings.Contains(resp.Error, path) {
		t.Errorf("error = %q, want it to name the file it could not write", resp.Error)
	}

	// The running daemon still behaves the way the user asked.
	got := d.Config().Priority
	if len(got) != 1 || got[0].Match != "JBL" {
		t.Errorf("in-memory priorities = %+v, want the entry that was sent", got)
	}
}

func TestSkipReasonNamesBothDevices(t *testing.T) {
	jbl := audio.Device{Name: "JBL Tune 520BT"}
	usb := audio.Device{Name: "USB Headset"}

	got := skipReason(jbl, usb, 2, 1, 3)
	if got != "skipping switch: JBL Tune 520BT is not ranked above USB Headset" {
		t.Errorf("ranked case = %q", got)
	}

	got = skipReason(jbl, usb, 3, 3, 3)
	if got != "skipping switch: neither JBL Tune 520BT nor USB Headset is on the priority list" {
		t.Errorf("unranked case = %q", got)
	}
}

func lastEvent(t *testing.T, d *Daemon) ipc.EventLog {
	t.Helper()
	events := d.Snapshot().Events
	if len(events) == 0 {
		t.Fatal("nothing was logged")
	}
	return events[len(events)-1]
}

// lockedBuffer collects what the standard logger writes. The daemon logs from
// several goroutines, and a plain bytes.Buffer races under -race.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
