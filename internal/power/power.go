// Package power reports logind's sleep and shutdown signals. Both remove
// sinks while the daemon runs, and that must not trigger a fallback.
package power

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// State is what the machine is about to do, or has just stopped doing.
type State int

const (
	// Awake means resumed, or logind cancelled an announced shutdown.
	Awake State = iota
	Sleeping
	ShuttingDown
)

func (s State) String() string {
	switch s {
	case Sleeping:
		return "sleeping"
	case ShuttingDown:
		return "shutting down"
	default:
		return "awake"
	}
}

const (
	managerPath  = "/org/freedesktop/login1"
	managerIface = "org.freedesktop.login1.Manager"
)

// Watch sends the machine's state each time logind announces a change. The
// channel closes when ctx ends or the bus connection drops.
func Watch(ctx context.Context) (<-chan State, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connecting to system D-Bus: %w", err)
	}
	for _, member := range []string{"PrepareForSleep", "PrepareForShutdown"} {
		if err := conn.AddMatchSignal(
			dbus.WithMatchObjectPath(managerPath),
			dbus.WithMatchInterface(managerIface),
			dbus.WithMatchMember(member),
		); err != nil {
			conn.Close()
			return nil, fmt.Errorf("adding D-Bus match for %s: %w", member, err)
		}
	}

	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)

	out := make(chan State, 2)
	go func() {
		defer close(out)
		defer conn.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				state, ok := parse(sig)
				if !ok {
					continue
				}
				select {
				case out <- state:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// Both logind signals carry true before going and false after coming back.
func parse(sig *dbus.Signal) (State, bool) {
	if len(sig.Body) < 1 {
		return Awake, false
	}
	starting, ok := sig.Body[0].(bool)
	if !ok {
		return Awake, false
	}

	var going State
	switch sig.Name {
	case managerIface + ".PrepareForSleep":
		going = Sleeping
	case managerIface + ".PrepareForShutdown":
		going = ShuttingDown
	default:
		return Awake, false
	}
	if !starting {
		return Awake, true
	}
	return going, true
}
