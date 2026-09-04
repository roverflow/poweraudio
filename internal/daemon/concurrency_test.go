package daemon

import (
	"context"
	"sync"
	"testing"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
)

// stubBackend is enough of an audio.Backend to drive the daemon without a
// sound server present.
type stubBackend struct {
	mu      sync.Mutex
	devices []audio.Device
	current string
}

func (b *stubBackend) Name() string { return "stub" }

func (b *stubBackend) ListSinks(context.Context) ([]audio.Device, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]audio.Device, len(b.devices))
	copy(out, b.devices)
	for i := range out {
		out[i].IsDefault = out[i].ID == b.current
	}
	return out, nil
}

func (b *stubBackend) GetDefaultSink(ctx context.Context) (*audio.Device, error) {
	sinks, _ := b.ListSinks(ctx)
	for i := range sinks {
		if sinks[i].IsDefault {
			return &sinks[i], nil
		}
	}
	return nil, nil
}

func (b *stubBackend) SetDefaultSink(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.current = id
	return nil
}

func (b *stubBackend) SetVolume(context.Context, string, int) error { return nil }
func (b *stubBackend) ToggleMute(context.Context, string) error     { return nil }
func (b *stubBackend) SubscribeEvents(context.Context) (<-chan audio.Event, error) {
	return nil, nil
}

// TestConfigAccessRace drives the reads the event goroutines perform against
// the writes an IPC request performs. Run under -race this fails if the config
// is read without the lock, which is what the pending-switch goroutine used
// to do.
func TestConfigAccessRace(t *testing.T) {
	backend := &stubBackend{devices: []audio.Device{
		{ID: "1", Name: "Speakers", Available: true},
		{ID: "2", Name: "JBL Tune 520BT", Type: audio.DeviceTypeBluetooth, Available: true},
	}}
	d := New(config.DefaultConfig(), backend, t.TempDir()+"/config.toml")
	ctx := context.Background()
	d.refreshDevices(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				_ = d.switching()
				_ = d.notifications()
				_ = d.priorities()
				_ = d.Config()
				_ = d.GetDevices()
				_ = d.hasPending()
				_ = d.hasDevice("1")
				_ = d.deviceName("2")
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				d.UpdatePriorities([]config.PriorityEntry{{Match: "JBL"}})
				d.logEvent("event %d/%d", n, j)
				_ = d.takeOwnSwitch("2")
			}
		}(i)
	}
	wg.Wait()
}

// TestSwitchesSerialize checks that two switch sequences cannot interleave,
// which is what leaves the output somewhere neither of them chose.
func TestSwitchesSerialize(t *testing.T) {
	backend := &stubBackend{devices: []audio.Device{
		{ID: "1", Name: "Speakers", Available: true},
		{ID: "2", Name: "JBL Tune 520BT", Type: audio.DeviceTypeBluetooth, Available: true},
	}}
	d := New(config.DefaultConfig(), backend, t.TempDir()+"/config.toml")
	ctx := context.Background()
	d.refreshDevices(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "1"
			if n%2 == 0 {
				id = "2"
			}
			if err := d.SetDefault(ctx, id); err != nil {
				t.Errorf("SetDefault(%s): %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	backend.mu.Lock()
	got := backend.current
	backend.mu.Unlock()
	if got != "1" && got != "2" {
		t.Fatalf("ended on sink %q, want one of the two that were requested", got)
	}
}

// TestEventLogIsBounded keeps the in-memory log from growing without limit on
// a daemon that has been up for weeks.
func TestEventLogIsBounded(t *testing.T) {
	d := New(config.DefaultConfig(), &stubBackend{}, t.TempDir()+"/config.toml")
	for i := 0; i < maxEvents*3; i++ {
		d.logEvent("event %d", i)
	}
	if got := len(d.GetEvents(0)); got != maxEvents {
		t.Errorf("kept %d events, want %d", got, maxEvents)
	}
	if got := len(d.GetEvents(10)); got != 10 {
		t.Errorf("asked for 10 events, got %d", got)
	}
	// The newest entry has to survive; it is the one you look at first.
	events := d.GetEvents(1)
	if len(events) != 1 || events[0].Message != "event 599" {
		t.Errorf("newest event = %v, want the last one logged", events)
	}
}
