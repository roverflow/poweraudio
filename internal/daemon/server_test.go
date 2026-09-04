package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
)

// serveIPC runs the request loop without the Bluetooth and audio
// subscriptions that Run would otherwise open.
func serveIPC(ctx context.Context, d *Daemon) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-d.ipcRequests:
				d.handleIPC(ctx, req)
			}
		}
	}()
}

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

func TestIPCRoundTrip(t *testing.T) {
	d, backend, socket := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d.refreshDevices(ctx)
	serveIPC(ctx, d)

	srv := NewServer(socket, d)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	client := ipc.NewClient(socket)

	if !client.Ping() {
		t.Fatal("daemon did not answer a ping")
	}

	devices, err := client.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("listed %d devices, want 2", len(devices))
	}

	status, err := client.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Backend != "stub" || status.Socket != socket {
		t.Errorf("status = %+v, want the stub backend on %s", status, socket)
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
}

// Unlinking the socket unconditionally let a second daemon take over from a
// live one, after which both fought over the default sink.
func TestSecondDaemonIsRefused(t *testing.T) {
	d, _, socket := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := NewServer(socket, d)
	if err := first.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer first.Close()

	second := NewServer(socket, d)
	err := second.Start(ctx)
	if err == nil {
		second.Close()
		t.Fatal("a second daemon was allowed to take the socket")
	}
	if !strings.Contains(err.Error(), "already listening") {
		t.Errorf("error = %v, want it to say another daemon is listening", err)
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

	serveIPC(ctx, d)
	srv := NewServer(socket, d)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

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
