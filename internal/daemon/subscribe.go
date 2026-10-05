package daemon

import (
	"context"
	"time"

	"github.com/roverflow/poweraudio/internal/ipc"
)

// coalesceWindow merges a burst of changes into one snapshot.
const coalesceWindow = 50 * time.Millisecond

// wake is buffered, so a client that stops reading cannot slow a switch.
type subscriber struct {
	wake chan struct{}
}

// Subscribe sends a snapshot now and after every change, coalescing bursts
// and keeping only the newest for a slow reader. It closes when ctx ends.
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
			// The snapshot below covers any poke left from the window.
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

// send replaces a pending snapshot instead of blocking or queueing.
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
