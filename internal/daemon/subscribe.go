package daemon

import (
	"context"
	"time"

	"github.com/roverflow/poweraudio/internal/ipc"
)

// coalesceWindow is how long a subscriber waits after the first change before
// building a snapshot. A refresh that logs three lines is one change to the
// person watching, and a volume drag is dozens.
const coalesceWindow = 50 * time.Millisecond

// subscriber is one open subscription. The daemon only ever pokes wake, which
// is buffered, so a client that has stopped reading cannot slow down a switch.
type subscriber struct {
	wake chan struct{}
}

// Subscribe returns a channel that carries a snapshot immediately and another
// one after every change to devices, events or config. Bursts are coalesced,
// and only the newest snapshot is kept for a subscriber that is behind: a UI
// wants the current state, not the backlog. The channel closes when ctx ends.
func (d *Daemon) Subscribe(ctx context.Context) <-chan ipc.Snapshot {
	sub := &subscriber{wake: make(chan struct{}, 1)}
	out := make(chan ipc.Snapshot, 1)

	d.subMu.Lock()
	d.subs[sub] = struct{}{}
	d.subMu.Unlock()

	go func() {
		defer close(out)
		defer func() {
			d.subMu.Lock()
			delete(d.subs, sub)
			d.subMu.Unlock()
		}()

		send(out, d.Snapshot())

		for {
			select {
			case <-ctx.Done():
				return
			case <-sub.wake:
			}

			if !sleepCtx(ctx, coalesceWindow) {
				return
			}
			// Everything that happened during the window is already in the
			// snapshot about to be built, so the pokes it left behind would
			// only produce a duplicate.
			select {
			case <-sub.wake:
			default:
			}

			send(out, d.Snapshot())
		}
	}()

	return out
}

// changed wakes every subscriber. It must not be called while holding mu,
// because a subscriber builds its snapshot under the read lock.
func (d *Daemon) changed() {
	d.subMu.RLock()
	defer d.subMu.RUnlock()
	for sub := range d.subs {
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

// send never blocks. When the reader is behind, the snapshot waiting for it is
// replaced rather than queued, so a stalled UI costs one stale snapshot rather
// than unbounded memory.
func send(out chan ipc.Snapshot, snap ipc.Snapshot) {
	select {
	case out <- snap:
		return
	default:
	}
	select {
	case <-out:
	default:
	}
	select {
	case out <- snap:
	default:
	}
}
