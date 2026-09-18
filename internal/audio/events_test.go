package audio

import "testing"

func TestParsePactlEvent(t *testing.T) {
	cases := []struct {
		line     string
		wantType EventType
		wantID   string
		wantOK   bool
	}{
		{"Event 'new' on sink #43", EventSinkAdded, "43", true},
		{"Event 'remove' on sink #43", EventSinkRemoved, "43", true},
		{"Event 'change' on sink #73", EventSinkChanged, "73", true},
		{"Event 'change' on server #0", EventDefaultChanged, "0", true},
		// A stream, not an output. These arrive whenever an application
		// starts or stops playing and used to look like sink changes.
		{"Event 'change' on sink-input #52", 0, "", false},
		{"Event 'new' on client #4779", 0, "", false},
		{"Event 'change' on card #57", 0, "", false},
		{"Got SIGINT, exiting.", 0, "", false},
		{"", 0, "", false},
	}
	for _, c := range cases {
		ev, ok := parsePactlEvent(c.line)
		if ok != c.wantOK {
			t.Errorf("parsePactlEvent(%q) ok = %v, want %v", c.line, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if ev.Type != c.wantType || ev.DeviceID != c.wantID {
			t.Errorf("parsePactlEvent(%q) = (%v, %q), want (%v, %q)",
				c.line, ev.Type, ev.DeviceID, c.wantType, c.wantID)
		}
	}
}
