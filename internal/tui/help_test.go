package tui

import (
	"strings"
	"testing"
)

// The reference used to be sized by hand to a twenty-four row terminal, and
// the Status section fell off the bottom as soon as it grew.
func TestHelpFitsATwentyFourRowTerminal(t *testing.T) {
	m := newHelpModel()
	m.width, m.height = 90, 24-chromeLines

	if got := m.rowCount(); got < len(helpRows) {
		t.Errorf("the help body gets %d rows for %d lines of keys", got, len(helpRows))
	}
}

func TestHelpScrollsWhenItDoesNotFit(t *testing.T) {
	m := newHelpModel()
	m.width, m.height = 60, 8

	if strings.Contains(stripANSI(m.View()), "install or remove the service") {
		t.Fatal("a short terminal somehow shows the whole reference")
	}

	for i := 0; i < len(helpRows); i++ {
		m, _ = m.scroll(1)
	}
	out := stripANSI(m.View())
	if !strings.Contains(out, "install or remove the service") {
		t.Errorf("scrolling never reached the last row:\n%s", out)
	}
	if !strings.Contains(out, "↑") {
		t.Errorf("no scroll hint while the body is scrolled:\n%s", out)
	}

	// Scrolling further cannot drag the last row off the top.
	before := m.offset
	m, _ = m.scroll(5)
	if m.offset != before {
		t.Errorf("offset ran past the end: %d, want %d", m.offset, before)
	}
}

func TestHelpDocumentsTheNewKeys(t *testing.T) {
	m := newHelpModel()
	m.width, m.height = 90, 20
	out := stripANSI(m.View())

	for _, want := range []string{"d c s", "mouse", "H L", "j/k scroll"} {
		if !strings.Contains(out, want) {
			t.Errorf("the reference never mentions %q:\n%s", want, out)
		}
	}
}
