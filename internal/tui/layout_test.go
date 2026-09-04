package tui

import (
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"Razer Barracuda X", 20, "Razer Barracuda X"},
		{"Razer Barracuda X", 17, "Razer Barracuda X"},
		{"Razer Barracuda X", 10, "Razer Bar…"},
		{"Razer", 1, "…"},
		{"Razer", 0, ""},
		{"Razer", -1, ""},
		// Runes, not bytes: cutting a multi-byte name by bytes would leave a
		// broken character behind and corrupt the row.
		{"Écouteurs Bluetooth", 6, "Écout…"},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestFitIsExactlyNCharacters(t *testing.T) {
	for _, in := range []string{"", "Razer", "Razer Barracuda X Analog Stereo", "Écouteurs"} {
		for _, n := range []int{1, 5, 12, 40} {
			if got := runeLen(fit(in, n)); got != n {
				t.Errorf("runeLen(fit(%q, %d)) = %d, want %d", in, n, got, n)
			}
		}
	}
}

func TestClampOffset(t *testing.T) {
	cases := []struct {
		name                             string
		offset, cursor, n, visible, want int
	}{
		{"everything fits", 0, 3, 5, 10, 0},
		{"cursor above the window", 5, 2, 20, 5, 2},
		{"cursor below the window", 0, 9, 20, 5, 5},
		{"cursor already inside", 4, 5, 20, 5, 4},
		{"offset past the end", 18, 0, 20, 5, 0},
		{"no rows to draw", 3, 3, 20, 0, 0},
		{"empty list", 3, 0, 0, 5, 0},
	}
	for _, c := range cases {
		got := clampOffset(c.offset, c.cursor, c.n, c.visible)
		if got != c.want {
			t.Errorf("%s: clampOffset(%d, %d, %d, %d) = %d, want %d",
				c.name, c.offset, c.cursor, c.n, c.visible, got, c.want)
		}
	}
}

func TestClampScroll(t *testing.T) {
	cases := []struct {
		offset, n, visible, want int
	}{
		{0, 20, 5, 0},
		{3, 20, 5, 3},
		{15, 20, 5, 15},
		{19, 20, 5, 15}, // the last row cannot be dragged to the top
		{-1, 20, 5, 0},
		{4, 3, 5, 0}, // shorter than the window
		{2, 0, 5, 0},
	}
	for _, c := range cases {
		if got := clampScroll(c.offset, c.n, c.visible); got != c.want {
			t.Errorf("clampScroll(%d, %d, %d) = %d, want %d",
				c.offset, c.n, c.visible, got, c.want)
		}
	}
}

func TestWindow(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}

	if got := window(lines, 1, 3); strings.Join(got, "") != "bcd" {
		t.Errorf("window(_, 1, 3) = %v", got)
	}
	if got := window(lines, 3, 5); strings.Join(got, "") != "de" {
		t.Errorf("window past the end = %v", got)
	}
	if got := window(lines, 9, 3); len(got) != 0 {
		t.Errorf("window entirely past the end = %v", got)
	}
	if got := window(lines, -1, 2); strings.Join(got, "") != "ab" {
		t.Errorf("window with a negative offset = %v", got)
	}
	if got := window(nil, 0, 3); got != nil {
		t.Errorf("window over nothing = %v", got)
	}
	if got := window(lines, 0, 0); got != nil {
		t.Errorf("window with no rows = %v", got)
	}
}

func TestFrameIsExactlyHeightLines(t *testing.T) {
	header := []string{"title", ""}
	footer := []string{"", "help"}

	for _, h := range []int{1, 4, 8, 24} {
		for _, bodyLen := range []int{0, 1, 10, 40} {
			body := make([]string, bodyLen)
			got := strings.Count(frame(h, header, body, footer), "\n") + 1
			if got != h {
				t.Errorf("frame(%d) with %d body lines produced %d lines", h, bodyLen, got)
			}
		}
	}
}

func TestFrameKeepsFooterOnTheBottomRow(t *testing.T) {
	out := frame(6, []string{"title"}, []string{"one", "two"}, []string{"help"})
	lines := strings.Split(out, "\n")
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6", len(lines))
	}
	if lines[0] != "title" {
		t.Errorf("first line = %q, want the header", lines[0])
	}
	if lines[5] != "help" {
		t.Errorf("last line = %q, want the footer", lines[5])
	}
	if lines[3] != "" || lines[4] != "" {
		t.Errorf("short body was not padded with blanks: %q", lines)
	}
}

func TestScrollHint(t *testing.T) {
	cases := []struct {
		offset, visible, n int
		want               string
	}{
		{0, 10, 5, ""},   // the whole list fits
		{0, 5, 20, "↓"},  // more below
		{15, 5, 20, "↑"}, // more above
		{5, 5, 20, "↑↓"}, // both
		{0, 0, 20, ""},
	}
	for _, c := range cases {
		if got := scrollHint(c.offset, c.visible, c.n); got != c.want {
			t.Errorf("scrollHint(%d, %d, %d) = %q, want %q",
				c.offset, c.visible, c.n, got, c.want)
		}
	}
}
