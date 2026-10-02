package notify

import (
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu   sync.Mutex
	got  []Notice
	done chan struct{}
}

func (r *recorder) deliver(n Notice) {
	r.mu.Lock()
	r.got = append(r.got, n)
	r.mu.Unlock()
	select {
	case r.done <- struct{}{}:
	default:
	}
}

func (r *recorder) notices() []Notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Notice(nil), r.got...)
}

// A fallback, the session manager's own pick and a second fallback within a
// few milliseconds are one change to the person at the desk.
func TestBurstCollapsesToTheLastNotice(t *testing.T) {
	r := &recorder{done: make(chan struct{}, 1)}
	n := newNotifier(20*time.Millisecond, r.deliver)
	defer n.Close()

	n.Show(Notice{Summary: "Playing on Ryzen"})
	n.Show(Notice{Summary: "Playing on Barracuda"})
	n.Show(Notice{Summary: "Playing on Ryzen", Body: "final"})

	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("nothing was delivered")
	}
	time.Sleep(40 * time.Millisecond)

	got := r.notices()
	if len(got) != 1 {
		t.Fatalf("delivered %d notices, want the burst collapsed into one: %v", len(got), got)
	}
	if got[0].Body != "final" {
		t.Errorf("delivered %+v, want the newest notice", got[0])
	}
}

func TestSeparateChangesAreBothShown(t *testing.T) {
	r := &recorder{done: make(chan struct{}, 2)}
	n := newNotifier(5*time.Millisecond, r.deliver)
	defer n.Close()

	n.Show(Notice{Summary: "one"})
	<-r.done
	n.Show(Notice{Summary: "two"})
	<-r.done

	if got := r.notices(); len(got) != 2 {
		t.Errorf("delivered %v, want both notices", got)
	}
}

func TestCloseDropsPendingNotice(t *testing.T) {
	r := &recorder{done: make(chan struct{}, 1)}
	n := newNotifier(10*time.Millisecond, r.deliver)
	n.Show(Notice{Summary: "late"})
	n.Close()
	n.Show(Notice{Summary: "after close"})

	time.Sleep(30 * time.Millisecond)
	if got := r.notices(); len(got) != 0 {
		t.Errorf("delivered %v after Close", got)
	}
}
