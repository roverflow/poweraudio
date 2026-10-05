package daemon

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/bluetooth"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
	"github.com/roverflow/poweraudio/internal/notify"
	"github.com/roverflow/poweraudio/internal/power"
	"github.com/roverflow/poweraudio/internal/razer"
)

const (
	jblID  = "bluez_output.3C_B0_ED_3A_2C_42.1"
	jblMAC = "3C:B0:ED:3A:2C:42"
)

func jbl() audio.Device {
	return audio.Device{ID: jblID, Name: "JBL Tune 520BT", Type: audio.DeviceTypeBluetooth, MACAddress: jblMAC, Available: true}
}

func placeholder() audio.Device {
	return audio.Device{ID: audio.PlaceholderID, Name: "Dummy Output", Available: true, Virtual: true}
}

type recorder struct {
	mu      sync.Mutex
	notices []notify.Notice
}

func (r *recorder) Show(n notify.Notice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, n)
}

func (r *recorder) got() []notify.Notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notify.Notice(nil), r.notices...)
}

// testDaemon is a daemon with notifications on, recorded, and no switch delay.
func testDaemon(t *testing.T, backend *stubBackend, edit func(*config.Config)) (*Daemon, *recorder) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Switching.SwitchDelayMs = 0
	if edit != nil {
		edit(&cfg)
	}
	d := New(cfg, backend, filepath.Join(t.TempDir(), "config.toml"))
	rec := &recorder{}
	d.notes = rec
	d.refreshDevices(context.Background())
	return d, rec
}

func connect(ctx context.Context, d *Daemon, mac, name string) {
	d.handleBluetoothEvent(ctx, bluetooth.Event{Connected: true, MACAddress: mac, DeviceName: name})
}

func disconnect(ctx context.Context, d *Daemon, mac, name string) {
	d.handleBluetoothEvent(ctx, bluetooth.Event{MACAddress: mac, DeviceName: name})
}

func hasLevel(d *Daemon, level ipc.Level) []string {
	var out []string
	for _, ev := range d.Snapshot().Events {
		if ev.Level == level {
			out = append(out, ev.Message)
		}
	}
	return out
}

// WirePlumber makes the headset default before the daemon looks. "previous"
// must still remember the output from before the headset.
func TestPreviousSurvivesTheSessionManagerSwitchingFirst(t *testing.T) {
	backend := &stubBackend{current: "hdmi", devices: []audio.Device{
		{ID: "ryzen", Name: "Ryzen", Available: true},
		{ID: "hdmi", Name: "HDMI", Available: true},
	}}
	d, _ := testDaemon(t, backend, func(c *config.Config) {
		c.Switching.OnDisconnect = "previous"
		c.Priority = []config.PriorityEntry{{Match: "Ryzen"}, {Match: "HDMI"}}
	})
	ctx := context.Background()

	backend.add(jbl())
	backend.setCurrent(jblID) // WirePlumber got there first
	connect(ctx, d, jblMAC, "JBL Tune 520BT")

	backend.remove(jblID)
	backend.setCurrent("ryzen") // then picks from its history on the way out
	disconnect(ctx, d, jblMAC, "JBL Tune 520BT")

	if got := backend.defaultID(); got != "hdmi" {
		t.Errorf("default = %q, want hdmi, which was playing before the headset", got)
	}
}

func TestAlreadyDefaultIsNotSkippedAsLowerRanked(t *testing.T) {
	backend := &stubBackend{current: "ryzen", devices: []audio.Device{{ID: "ryzen", Name: "Ryzen", Available: true}}}
	d, rec := testDaemon(t, backend, func(c *config.Config) {
		c.Switching.OnConnect = "priority"
		c.Priority = []config.PriorityEntry{{Match: "JBL"}, {Match: "Ryzen"}}
	})
	ctx := context.Background()

	backend.add(jbl())
	backend.setCurrent(jblID)
	connect(ctx, d, jblMAC, "JBL Tune 520BT")

	for _, ev := range d.Snapshot().Events {
		if strings.Contains(ev.Message, "not ranked above") {
			t.Errorf("logged %q for a device that was already the default", ev.Message)
		}
	}
	if sets := backend.switches(); len(sets) != 0 {
		t.Errorf("switched %v onto a device that was already the default", sets)
	}
	notices := rec.got()
	if len(notices) != 1 || notices[0].Body != reasonConnect.body() {
		t.Errorf("notices = %+v, want one announcing the connect", notices)
	}
}

func TestMouseDoesNotCancelAHeadsetSwitch(t *testing.T) {
	backend := &stubBackend{current: "ryzen", devices: []audio.Device{{ID: "ryzen", Name: "Ryzen", Available: true}}}
	d, _ := testDaemon(t, backend, nil)
	ctx := context.Background()

	connect(ctx, d, jblMAC, "JBL Tune 520BT")
	d.handleBluetoothEvent(ctx, bluetooth.Event{Connected: true, MACAddress: "11:22:33:44:55:66", DeviceName: "MX Master 3", NotAudio: true})
	// Even a device BlueZ could not describe gets its own slot.
	connect(ctx, d, "AA:BB:CC:DD:EE:FF", "Unknown")

	backend.add(jbl())
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkAdded})

	if got := backend.defaultID(); got != jblID {
		t.Errorf("default = %q, want the headset whose sink just appeared", got)
	}
	for _, msg := range hasLevel(d, ipc.LevelInfo) {
		if strings.Contains(msg, "MX Master") {
			t.Errorf("a mouse connecting was logged at info: %q", msg)
		}
	}
}

// PipeWire answers "Not supported" to a switch onto the placeholder.
func TestPlaceholderIsNeverPicked(t *testing.T) {
	backend := &stubBackend{current: "ryzen", devices: []audio.Device{{ID: "ryzen", Name: "Ryzen", Available: true}}}
	backend.refuse = map[string]bool{audio.PlaceholderID: true}
	d, rec := testDaemon(t, backend, nil)
	ctx := context.Background()

	backend.remove("ryzen")
	backend.add(placeholder())
	backend.setCurrent(audio.PlaceholderID)
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkRemoved})

	if sets := backend.switches(); len(sets) != 0 {
		t.Errorf("switched %v with only the placeholder left", sets)
	}
	if errs := hasLevel(d, ipc.LevelError); len(errs) != 0 {
		t.Errorf("errors logged: %v", errs)
	}
	if n := rec.got(); len(n) != 0 {
		t.Errorf("announced %+v for the placeholder", n)
	}

	backend.add(audio.Device{ID: "ryzen", Name: "Ryzen", Available: true})
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkAdded})
	if got := backend.defaultID(); got != "ryzen" {
		t.Errorf("default = %q, want the output that came back", got)
	}
}

// WirePlumber can restore the Barracuda after a resume with its earcups off.
func TestExternalSwitchOntoAHeadsetThatIsOffFallsBack(t *testing.T) {
	backend := barracudaSinks(t, "ryzen")
	d, _ := testDaemon(t, backend, nil)
	ctx := context.Background()
	d.handleEarcups(ctx, false)

	backend.setCurrent("barracuda")
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventDefaultChanged})

	if got := backend.defaultID(); got != "ryzen" {
		t.Errorf("default = %q, want it moved off the powered-off headset", got)
	}
}

func TestStartupHoldLeavesTheSessionManagersPick(t *testing.T) {
	backend := &stubBackend{current: audio.PlaceholderID, devices: []audio.Device{placeholder()}}
	d, rec := testDaemon(t, backend, func(c *config.Config) {
		c.Priority = []config.PriorityEntry{{Match: "Ryzen"}, {Match: "HDMI"}}
	})
	ctx := context.Background()
	d.setHolding(true)

	backend.add(audio.Device{ID: "hdmi", Name: "HDMI", Type: audio.DeviceTypeHDMI, Available: true})
	backend.setCurrent("hdmi")
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkAdded})
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventDefaultChanged})

	if sets := backend.switches(); len(sets) != 0 {
		t.Fatalf("switched %v during the hold", sets)
	}

	backend.add(audio.Device{ID: "ryzen", Name: "Ryzen", Available: true})
	backend.setCurrent("ryzen") // WirePlumber's own saved choice turning up
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkAdded})
	d.endHold(ctx, "")

	if sets := backend.switches(); len(sets) != 0 {
		t.Errorf("switched %v at the end of a hold onto a default that can play", sets)
	}
	if n := rec.got(); len(n) != 0 {
		t.Errorf("announced %+v at login", n)
	}
}

func TestStartupHoldStillMovesOffAnUnusableDefault(t *testing.T) {
	backend := &stubBackend{current: audio.PlaceholderID, devices: []audio.Device{placeholder()}}
	d, _ := testDaemon(t, backend, func(c *config.Config) {
		c.Priority = []config.PriorityEntry{{Match: "Ryzen"}, {Match: "HDMI"}}
	})
	ctx := context.Background()
	d.setHolding(true)

	backend.add(audio.Device{ID: "hdmi", Name: "HDMI", Available: true})
	backend.add(audio.Device{ID: "ryzen", Name: "Ryzen", Available: true})
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkAdded})
	d.endHold(ctx, "")

	if got := backend.defaultID(); got != "ryzen" {
		t.Errorf("default = %q, want the top of the ranking once the hold ends", got)
	}
}

func TestNotificationsByReason(t *testing.T) {
	ctx := context.Background()

	t.Run("fallback", func(t *testing.T) {
		backend := rankedSinks(t, "barracuda")
		d, rec := testDaemon(t, backend, func(c *config.Config) {
			c.Priority = []config.PriorityEntry{{Match: "Barracuda"}, {Match: "Headphones"}}
		})
		backend.remove("barracuda")
		d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkRemoved})

		n := rec.got()
		if len(n) != 1 || n[0].Summary != "Playing on Headphones" || n[0].Body != reasonFallback.body() {
			t.Errorf("notices = %+v, want one fallback notice", n)
		}
	})

	t.Run("manual is quiet unless asked", func(t *testing.T) {
		backend := rankedSinks(t, "barracuda")
		d, rec := testDaemon(t, backend, nil)
		if err := d.SetDefault(ctx, "headset", false); err != nil {
			t.Fatal(err)
		}
		if n := rec.got(); len(n) != 0 {
			t.Errorf("a switch from the UI was announced: %+v", n)
		}
		if err := d.SetDefault(ctx, "ryzen", true); err != nil {
			t.Fatal(err)
		}
		if n := rec.got(); len(n) != 1 || n[0].Body != reasonManual.body() {
			t.Errorf("notices = %+v, want one for the switch that asked", n)
		}
	})

	t.Run("external", func(t *testing.T) {
		backend := rankedSinks(t, "barracuda")
		d, rec := testDaemon(t, backend, nil)
		backend.setCurrent("ryzen")
		d.handleAudioEvent(ctx, audio.Event{Type: audio.EventDefaultChanged})
		if n := rec.got(); len(n) != 1 || n[0].Body != reasonExternal.body() {
			t.Errorf("notices = %+v, want one for a change made elsewhere", n)
		}
	})

	// WirePlumber moves off a headset before the daemon's own fallback runs.
	t.Run("session manager falls back after a disconnect", func(t *testing.T) {
		backend := &stubBackend{current: jblID, devices: []audio.Device{
			{ID: "ryzen", Name: "Ryzen", Available: true},
			jbl(),
		}}
		d, rec := testDaemon(t, backend, nil)
		backend.remove(jblID)
		backend.setCurrent("ryzen")
		disconnect(ctx, d, jblMAC, "JBL Tune 520BT")

		if n := rec.got(); len(n) != 1 || n[0].Body != reasonFallback.body() {
			t.Errorf("notices = %+v, want one fallback notice", n)
		}
		want := "default device changed to Ryzen after JBL Tune 520BT went away"
		if got := hasLevel(d, ipc.LevelInfo); !slices.Contains(got, want) {
			t.Errorf("info lines = %q, want %q", got, want)
		}
	})

	// The headset's sink can stay listed for a moment after the link drops.
	t.Run("session manager falls back before the sink goes", func(t *testing.T) {
		backend := &stubBackend{current: jblID, devices: []audio.Device{
			{ID: "ryzen", Name: "Ryzen", Available: true},
			jbl(),
		}}
		d, rec := testDaemon(t, backend, nil)
		d.noteDisconnect(jblMAC)
		backend.setCurrent("ryzen")
		d.handleAudioEvent(ctx, audio.Event{Type: audio.EventDefaultChanged})

		if n := rec.got(); len(n) != 1 || n[0].Body != reasonFallback.body() {
			t.Errorf("notices = %+v, want one fallback notice", n)
		}
	})

	t.Run("external after an idle headset leaves", func(t *testing.T) {
		backend := &stubBackend{current: "ryzen", devices: []audio.Device{
			{ID: "ryzen", Name: "Ryzen", Available: true},
			{ID: "hdmi", Name: "HDMI", Available: true},
			jbl(),
		}}
		d, rec := testDaemon(t, backend, nil)
		d.noteDisconnect(jblMAC)
		backend.setCurrent("hdmi")
		d.handleAudioEvent(ctx, audio.Event{Type: audio.EventDefaultChanged})

		if n := rec.got(); len(n) != 1 || n[0].Body != reasonExternal.body() {
			t.Errorf("notices = %+v, want one for a change made elsewhere", n)
		}
	})

	t.Run("external with on_device_change off", func(t *testing.T) {
		backend := rankedSinks(t, "barracuda")
		d, rec := testDaemon(t, backend, func(c *config.Config) { c.Notifications.OnDeviceChange = false })
		backend.setCurrent("ryzen")
		d.handleAudioEvent(ctx, audio.Event{Type: audio.EventDefaultChanged})
		if n := rec.got(); len(n) != 0 {
			t.Errorf("notices = %+v with on_device_change off", n)
		}
	})

	t.Run("icon follows the device", func(t *testing.T) {
		backend := &stubBackend{current: "ryzen", devices: []audio.Device{
			{ID: "ryzen", Name: "Ryzen", Type: audio.DeviceTypeSpeaker, Available: true},
			{ID: "hdmi", Name: "Monitor", Type: audio.DeviceTypeHDMI, Available: true},
		}}
		d, rec := testDaemon(t, backend, nil)
		if err := d.SetDefault(ctx, "hdmi", true); err != nil {
			t.Fatal(err)
		}
		if n := rec.got(); len(n) != 1 || n[0].Icon != "video-display" {
			t.Errorf("notices = %+v, want the display icon for HDMI", n)
		}
	})
}

func TestDefaultChangeReadsOnlyTheDefault(t *testing.T) {
	backend := rankedSinks(t, "barracuda")
	d, _ := testDaemon(t, backend, nil)
	before := backend.listCount()

	backend.setCurrent("ryzen")
	d.handleAudioEvent(context.Background(), audio.Event{Type: audio.EventDefaultChanged})

	if got := backend.listCount() - before; got != 0 {
		t.Errorf("listed sinks %d times for a default change", got)
	}
	if got := d.defaultID(); got != "ryzen" {
		t.Errorf("cached default = %q, want ryzen", got)
	}
}

func TestUnchangedSinkListDoesNotWakeSubscribers(t *testing.T) {
	backend := rankedSinks(t, "barracuda")
	d, _ := testDaemon(t, backend, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := d.Subscribe(ctx)
	<-stream // the snapshot every subscriber gets first

	d.refreshDevices(ctx)
	d.refreshDevices(ctx)
	select {
	case <-stream:
		t.Error("a refresh that changed nothing sent a snapshot")
	case <-time.After(3 * coalesceWindow):
	}

	backend.setAvailable("ryzen", false)
	d.refreshDevices(ctx)
	select {
	case <-stream:
	case <-time.After(time.Second):
		t.Error("a refresh that changed a sink sent nothing")
	}
}

func TestEarlyBarracudaPermissionErrorIsQuiet(t *testing.T) {
	d, _ := testDaemon(t, rankedSinks(t, "barracuda"), nil)
	d.handleLinkEvent(context.Background(), razer.Event{Path: "/dev/hidraw4", Err: errPermission})
	if warns := hasLevel(d, ipc.LevelWarn); len(warns) != 0 {
		t.Errorf("a permission error at startup was a warning: %v", warns)
	}

	d.startTime = time.Now().Add(-2 * quietStart)
	d.handleLinkEvent(context.Background(), razer.Event{Path: "/dev/hidraw4", Err: errPermission})
	if warns := hasLevel(d, ipc.LevelWarn); len(warns) != 1 {
		t.Errorf("a permission error long after startup was not a warning: %v", warns)
	}
}

var errPermission = &permissionError{}

type permissionError struct{}

func (*permissionError) Error() string { return "open /dev/hidraw4: permission denied" }

// flakyBackend's first stream closes at once, like pactl subscribe on restart.
type flakyBackend struct {
	*stubBackend
	mu    sync.Mutex
	calls int
	live  chan audio.Event
}

func (b *flakyBackend) SubscribeEvents(context.Context) (<-chan audio.Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.calls == 1 {
		ch := make(chan audio.Event)
		close(ch)
		return ch, nil
	}
	return b.live, nil
}

func (b *flakyBackend) subscriptions() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func shortTimers(t *testing.T) {
	t.Helper()
	saved := []time.Duration{holdStable, holdStartupMax, holdResumeStable, holdResumeMax, resubscribeMin}
	holdStable, holdStartupMax = 20*time.Millisecond, 200*time.Millisecond
	holdResumeStable, holdResumeMax = 20*time.Millisecond, 200*time.Millisecond
	resubscribeMin = 10 * time.Millisecond
	t.Cleanup(func() {
		holdStable, holdStartupMax = saved[0], saved[1]
		holdResumeStable, holdResumeMax = saved[2], saved[3]
		resubscribeMin = saved[4]
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAudioEventStreamIsFollowedAgain(t *testing.T) {
	shortTimers(t)
	backend := &flakyBackend{stubBackend: rankedSinks(t, "barracuda"), live: make(chan audio.Event, 4)}
	d := New(config.DefaultConfig(), backend, filepath.Join(t.TempDir(), "config.toml"))
	d.notes = &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.runEvents(ctx, sources{})

	waitFor(t, "a second subscription", func() bool { return backend.subscriptions() >= 2 })

	waitFor(t, "the startup hold to end", func() bool { return !d.isHolding() })
	backend.remove("barracuda")
	backend.live <- audio.Event{Type: audio.EventSinkRemoved}
	waitFor(t, "the fallback", func() bool { return backend.defaultID() != "barracuda" })

	var reconnected bool
	for _, ev := range d.Snapshot().Events {
		reconnected = reconnected || strings.Contains(ev.Message, "reconnected")
	}
	if !reconnected {
		t.Error("the reconnect was not logged")
	}
}

func TestSleepHoldsSwitchesUntilResumeSettles(t *testing.T) {
	shortTimers(t)
	backend := &stubBackend{current: jblID, devices: []audio.Device{
		jbl(),
		{ID: "ryzen", Name: "Ryzen", Available: true},
	}}
	rec := &recorder{}
	cfg := config.DefaultConfig()
	cfg.Priority = []config.PriorityEntry{{Match: "JBL"}, {Match: "Ryzen"}}
	d := New(cfg, backend, filepath.Join(t.TempDir(), "config.toml"))
	d.notes = rec

	events := make(chan audio.Event, 8)
	states := make(chan power.State, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.runEvents(ctx, sources{audio: events, power: states})
	waitFor(t, "the startup hold to end", func() bool { return !d.isHolding() })

	states <- power.Sleeping
	waitFor(t, "the sleep hold", d.isHolding)

	backend.remove(jblID)
	backend.remove("ryzen")
	backend.add(placeholder())
	backend.setCurrent(audio.PlaceholderID)
	events <- audio.Event{Type: audio.EventSinkRemoved}
	events <- audio.Event{Type: audio.EventDefaultChanged}
	time.Sleep(50 * time.Millisecond)
	if sets := backend.switches(); len(sets) != 0 {
		t.Fatalf("switched %v while the machine was going to sleep", sets)
	}

	// On resume the speakers return and the headset does not.
	backend.remove(audio.PlaceholderID)
	backend.add(audio.Device{ID: "ryzen", Name: "Ryzen", Available: true})
	states <- power.Awake
	events <- audio.Event{Type: audio.EventSinkAdded}

	waitFor(t, "the fallback after resume", func() bool { return backend.defaultID() == "ryzen" })
	waitFor(t, "the resume notice", func() bool { return len(rec.got()) > 0 })
	if sets := backend.switches(); !slices.Equal(sets, []string{"ryzen"}) {
		t.Errorf("switches = %v, want a single move to ryzen", sets)
	}
	n := rec.got()
	if n[len(n)-1].Summary != "Playing on Ryzen" {
		t.Errorf("notices = %+v, want the resume to end on Ryzen", n)
		for _, ev := range d.Snapshot().Events {
			t.Logf("%s %s", ev.Level, ev.Message)
		}
	}
}

// At reboot every sink vanishes a second before systemd stops the daemon.
func TestShutdownHoldsSwitches(t *testing.T) {
	shortTimers(t)
	backend := &stubBackend{current: "ryzen", devices: []audio.Device{
		{ID: "ryzen", Name: "Ryzen", Available: true},
		{ID: "hdmi", Name: "HDMI", Available: true},
	}}
	d := New(config.DefaultConfig(), backend, filepath.Join(t.TempDir(), "config.toml"))
	rec := &recorder{}
	d.notes = rec

	events := make(chan audio.Event, 8)
	states := make(chan power.State, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.runEvents(ctx, sources{audio: events, power: states})
	waitFor(t, "the startup hold to end", func() bool { return !d.isHolding() })

	states <- power.ShuttingDown
	waitFor(t, "the shutdown hold", d.isHolding)

	backend.remove("ryzen")
	backend.remove("hdmi")
	backend.add(placeholder())
	backend.setCurrent(audio.PlaceholderID)
	for range 3 {
		events <- audio.Event{Type: audio.EventSinkRemoved}
	}
	events <- audio.Event{Type: audio.EventDefaultChanged}
	time.Sleep(4 * holdResumeStable)

	if !d.isHolding() {
		t.Error("the shutdown hold ended on its own")
	}
	if sets := backend.switches(); len(sets) != 0 {
		t.Errorf("switched %v while shutting down", sets)
	}
	if n := rec.got(); len(n) != 0 {
		t.Errorf("announced %+v while shutting down", n)
	}
	for _, ev := range d.Snapshot().Events {
		if ev.Level == ipc.LevelWarn || strings.Contains(ev.Message, "no output left") {
			t.Errorf("logged %s %q while shutting down", ev.Level, ev.Message)
		}
	}
}

func TestCancelledShutdownResumesSwitching(t *testing.T) {
	shortTimers(t)
	backend := rankedSinks(t, "barracuda")
	d := New(config.DefaultConfig(), backend, filepath.Join(t.TempDir(), "config.toml"))
	d.notes = &recorder{}

	events := make(chan audio.Event, 4)
	states := make(chan power.State, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.runEvents(ctx, sources{audio: events, power: states})
	waitFor(t, "the startup hold to end", func() bool { return !d.isHolding() })

	states <- power.ShuttingDown
	waitFor(t, "the shutdown hold", d.isHolding)
	states <- power.Awake
	waitFor(t, "the hold to end after the cancel", func() bool { return !d.isHolding() })

	backend.remove("barracuda")
	events <- audio.Event{Type: audio.EventSinkRemoved}
	waitFor(t, "the fallback", func() bool { return backend.defaultID() != "barracuda" })
}

// Logging out removes every sink with no shutdown announced.
func TestNoOutputLeftIsSaidOncePerOutage(t *testing.T) {
	backend := &stubBackend{current: "ryzen", devices: []audio.Device{
		{ID: "ryzen", Name: "Ryzen", Available: true},
		{ID: "hdmi", Name: "HDMI", Available: true},
		{ID: "kreo", Name: "KREO", Available: true},
	}}
	d, _ := testDaemon(t, backend, nil)
	ctx := context.Background()

	stranded := func() int {
		n := 0
		for _, ev := range d.Snapshot().Events {
			if strings.Contains(ev.Message, "no output left") {
				n++
				if ev.Level != ipc.LevelInfo {
					t.Errorf("logged %q at %s, want info", ev.Message, ev.Level)
				}
			}
		}
		return n
	}

	backend.add(placeholder())
	for _, id := range []string{"ryzen", "hdmi", "kreo"} {
		backend.remove(id)
		backend.setCurrent(audio.PlaceholderID)
		d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkRemoved})
	}
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventDefaultChanged})
	if got := stranded(); got != 1 {
		t.Fatalf("said there was no output %d times in one outage, want once", got)
	}

	// An output comes back and then goes again, which is a second outage.
	backend.add(audio.Device{ID: "ryzen", Name: "Ryzen", Available: true})
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkAdded})
	if got := backend.defaultID(); got != "ryzen" {
		t.Fatalf("default = %q, want the output that came back", got)
	}
	backend.remove("ryzen")
	backend.setCurrent(audio.PlaceholderID)
	d.handleAudioEvent(ctx, audio.Event{Type: audio.EventSinkRemoved})
	if got := stranded(); got != 2 {
		t.Errorf("said there was no output %d times over two outages, want twice", got)
	}
}
