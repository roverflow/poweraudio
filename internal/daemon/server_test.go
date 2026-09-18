package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
)

func newTestDaemon(t *testing.T) (*Daemon, *stubBackend, string) {
	t.Helper()
	dir := t.TempDir()
	backend := &stubBackend{
		devices: []audio.Device{
			{ID: "1", Name: "Speakers", Available: true},
			{ID: "2", Name: "JBL Tune 520BT", Type: audio.DeviceTypeBluetooth, Available: true},
		},
		current: "1",
	}
	cfg := config.DefaultConfig()
	cfg.Daemon.SocketPath = filepath.Join(dir, "poweraudio.sock")
	return New(cfg, backend, filepath.Join(dir, "config.toml")), backend, cfg.Daemon.SocketPath
}

func startServer(t *testing.T, ctx context.Context, d *Daemon, socket string) {
	t.Helper()
	srv := NewServer(socket, d)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Close)
}

// One round trip has to carry everything a screen draws.
func TestIPCRoundTrip(t *testing.T) {
	d, backend, socket := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d.refreshDevices(ctx)
	startServer(t, ctx, d, socket)

	client := ipc.NewClient(socket)

	if !client.Ping() {
		t.Fatal("daemon did not answer a ping")
	}

	snap, err := client.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Devices) != 2 {
		t.Fatalf("listed %d devices, want 2", len(snap.Devices))
	}
	if snap.Status.Backend != "stub" || snap.Status.ConfigPath != d.ConfigPath() {
		t.Errorf("status = %+v, want the stub backend reading %s", snap.Status, d.ConfigPath())
	}
	if snap.Status.StartedAt.IsZero() {
		t.Error("status has no start time")
	}
	if dev := snap.Default(); dev == nil || dev.ID != "1" {
		t.Errorf("default = %v, want the sink the backend is on", dev)
	}

	if err := client.SetDefault("2"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	backend.mu.Lock()
	current := backend.current
	backend.mu.Unlock()
	if current != "2" {
		t.Errorf("backend default = %q, want %q", current, "2")
	}

	// Saving has to land in the file this daemon was started with.
	if err := client.UpdatePriorities([]config.PriorityEntry{{Match: "JBL"}}); err != nil {
		t.Fatalf("UpdatePriorities: %v", err)
	}
	saved, err := config.Load(d.ConfigPath())
	if err != nil {
		t.Fatalf("reading back the config: %v", err)
	}
	if len(saved.Priority) != 1 || saved.Priority[0].Match != "JBL" {
		t.Errorf("saved priorities = %+v", saved.Priority)
	}

	// The next snapshot shows both changes.
	snap, err = client.Snapshot()
	if err != nil {
		t.Fatalf("second Snapshot: %v", err)
	}
	if dev := snap.Default(); dev == nil || dev.ID != "2" {
		t.Errorf("default = %v, want the sink that was just selected", dev)
	}
	if len(snap.Config.Priority) != 1 {
		t.Errorf("snapshot config = %+v, want the new ranking", snap.Config.Priority)
	}
	if len(snap.Events) == 0 {
		t.Error("snapshot carries no events")
	}
}

// A subscriber gets the state now and then every change, so the UI does not
// have to poll for one.
func TestSubscribeStreamsChanges(t *testing.T) {
	d, _, socket := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d.refreshDevices(ctx)
	startServer(t, ctx, d, socket)

	client := ipc.NewClient(socket)
	snaps, err := client.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	first := receive(t, snaps)
	if dev := first.Default(); dev == nil || dev.ID != "1" {
		t.Fatalf("first snapshot default = %v, want the sink the backend is on", dev)
	}

	params, err := json.Marshal(ipc.SetDefaultParams{DeviceID: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if resp := d.Handle(ctx, ipc.Request{Method: ipc.MethodSetDefault, Params: params}); !resp.OK {
		t.Fatalf("set_default: %s", resp.Error)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case snap, ok := <-snaps:
			if !ok {
				t.Fatal("the subscription closed before the change arrived")
			}
			if dev := snap.Default(); dev != nil && dev.ID == "2" {
				return
			}
		case <-deadline:
			t.Fatal("no snapshot showed the new default")
		}
	}
}

// Unlinking the socket unconditionally let a second daemon take over from a
// live one, after which both fought over the default sink.
func TestSecondDaemonIsRefused(t *testing.T) {
	d, _, socket := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startServer(t, ctx, d, socket)

	second := NewServer(socket, d)
	err := second.Start(ctx)
	if err == nil {
		second.Close()
		t.Fatal("a second daemon was allowed to take the socket")
	}
	// main.go exits zero on this one, so it has to be recognisable rather than
	// only readable.
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("error = %v, want ErrAlreadyRunning", err)
	}
	if !strings.Contains(err.Error(), socket) {
		t.Errorf("error = %v, want it to name the socket", err)
	}

	// The first one has to still be there afterwards.
	conn, dialErr := net.Dial("unix", socket)
	if dialErr != nil {
		t.Fatalf("the original daemon lost its socket: %v", dialErr)
	}
	conn.Close()
}

// A socket file left behind by a crash has nothing behind it, so starting over
// it is fine.
func TestStaleSocketIsReplaced(t *testing.T) {
	d, _, socket := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stale, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	stale.Close()
	os.Remove(socket)
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(socket, d)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start over a stale socket: %v", err)
	}
	srv.Close()
}

func TestUnknownMethodIsRejected(t *testing.T) {
	d, _, socket := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startServer(t, ctx, d, socket)

	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(`{"method":"do_something_else"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf[:n]), "unknown method") {
		t.Errorf("response = %s, want an unknown method error", buf[:n])
	}
}

func receive(t *testing.T, ch <-chan ipc.Snapshot) ipc.Snapshot {
	t.Helper()
	select {
	case snap, ok := <-ch:
		if !ok {
			t.Fatal("the subscription closed without sending anything")
		}
		return snap
	case <-time.After(5 * time.Second):
		t.Fatal("no snapshot arrived")
		return ipc.Snapshot{}
	}
}
