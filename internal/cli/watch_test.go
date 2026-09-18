package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/roverflow/poweraudio/internal/ipc"
)

// streamOf is a closed channel already holding every snapshot, which is the
// daemon sending a burst and then going away.
func streamOf(snaps ...ipc.Snapshot) chan ipc.Snapshot {
	ch := make(chan ipc.Snapshot, len(snaps))
	for _, snap := range snaps {
		ch <- snap
	}
	close(ch)
	return ch
}

func TestWatchPrintsOneLinePerChange(t *testing.T) {
	quiet := fixture()
	quiet.Devices[1].Volume = 0.2
	muted := fixture()
	muted.Devices[1].Muted = true

	// The middle snapshot repeats the first one, which is what a logged event
	// with no audible change looks like.
	stream := streamOf(fixture(), fixture(), quiet, muted)

	var out bytes.Buffer
	err := watch(context.Background(), stream, &out, false)
	if err == nil || err.Error() != "daemon went away" {
		t.Fatalf("watch returned %v, want the daemon going away to be an error", err)
	}

	want := "JBL Tune 520BT  80%\nJBL Tune 520BT  20%\nJBL Tune 520BT  muted\n"
	if out.String() != want {
		t.Errorf("watch wrote %q, want %q", out.String(), want)
	}
}

func TestWatchJSONKeepsEverySnapshot(t *testing.T) {
	var out bytes.Buffer
	if err := watch(context.Background(), streamOf(fixture(), fixture()), &out, true); err == nil {
		t.Fatal("watch returned nil, want the daemon going away to be an error")
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("watch wrote %d lines, want one per snapshot", len(lines))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, `{"devices":`) {
			t.Errorf("line = %q, want one compact json object", line)
		}
		if strings.Contains(line, "\n") {
			t.Errorf("line = %q, want it to stay on one line", line)
		}
	}
}

func TestWatchStopsQuietlyOnASignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A cancelled context is the signal handler having fired, and the stream
	// closing after it is not a failure.
	var out bytes.Buffer
	if err := watch(ctx, streamOf(), &out, false); err != nil {
		t.Errorf("watch returned %v, want nil after a signal", err)
	}
}

func TestWatchCommandReportsTheDaemonGoingAway(t *testing.T) {
	changed := fixture()
	changed.Devices[1].Volume = 0.2

	fake := newFake()
	fake.stream = streamOf(fixture(), changed)

	code, stdout, stderr := exec(fake, "watch")
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if stdout != "JBL Tune 520BT  80%\nJBL Tune 520BT  20%\n" {
		t.Errorf("stdout = %q, want a line per change", stdout)
	}
	if !strings.Contains(stderr, "daemon went away") {
		t.Errorf("stderr = %q, want it to say the daemon went away", stderr)
	}
}
