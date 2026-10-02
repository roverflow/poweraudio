package power

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name   string
		signal string
		body   []any
		want   State
		ok     bool
	}{
		{"going to sleep", "PrepareForSleep", []any{true}, Sleeping, true},
		{"resumed", "PrepareForSleep", []any{false}, Awake, true},
		{"shutting down", "PrepareForShutdown", []any{true}, ShuttingDown, true},
		{"shutdown cancelled", "PrepareForShutdown", []any{false}, Awake, true},
		{"another logind signal", "SessionNew", []any{"2", dbus.ObjectPath("/org/freedesktop/login1/session/_32")}, Awake, false},
		{"no body", "PrepareForSleep", nil, Awake, false},
		{"wrong type", "PrepareForShutdown", []any{"yes"}, Awake, false},
	}
	for _, c := range cases {
		sig := &dbus.Signal{Name: managerIface + "." + c.signal, Body: c.body}
		got, ok := parse(sig)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: parse = %v, %v; want %v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
