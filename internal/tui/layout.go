package tui

import "strings"

const (
	// Sizes used before the first resize message arrives.
	defaultWidth  = 80
	defaultHeight = 24

	// Floors. Below these the layout stops shrinking and starts clipping,
	// which is uglier but never wraps and corrupts the frame.
	minWidth    = 40
	minContentH = 8

	// Lines Model.View spends outside the screen content: the tab bar, a
	// blank line, another blank line and the status bar.
	chromeLines = 4
)

// runeLen counts characters rather than bytes so that padding and truncation
// line up for device names that are not pure ASCII.
func runeLen(s string) int {
	return len([]rune(s))
}

// truncate shortens s to at most n characters and marks the cut with an
// ellipsis. It slices runes, so a multi-byte name never breaks mid-character.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// padRight pads s with spaces out to n characters.
func padRight(s string, n int) string {
	if d := n - runeLen(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// fit truncates then pads, so every cell in a column occupies exactly n
// characters no matter what went into it.
func fit(s string, n int) string {
	return padRight(truncate(s, n), n)
}

// clampOffset moves a scroll offset the shortest distance that brings cursor
// inside a window of visible rows over n items.
func clampOffset(offset, cursor, n, visible int) int {
	if visible <= 0 || n <= 0 {
		return 0
	}
	if limit := n - visible; offset > limit {
		offset = limit
	}
	if offset < 0 {
		offset = 0
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+visible {
		offset = cursor - visible + 1
	}
	return offset
}

// clampScroll bounds a cursorless scroll offset, so the last row of a list
// cannot be dragged up past the top of the window leaving mostly blank space.
func clampScroll(offset, n, visible int) int {
	limit := n - visible
	if limit < 0 {
		limit = 0
	}
	if offset > limit {
		offset = limit
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// window returns the run of lines starting at offset that fits in visible rows.
func window(lines []string, offset, visible int) []string {
	if visible <= 0 || len(lines) == 0 {
		return nil
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(lines) {
		offset = len(lines)
	}
	end := offset + visible
	if end > len(lines) {
		end = len(lines)
	}
	return lines[offset:end]
}

// frame stacks header, body and footer into exactly height lines. The body is
// padded with blanks or clipped, so the footer always lands on the bottom row
// and every screen returns the same number of lines the caller budgeted for.
func frame(height int, header, body, footer []string) string {
	if height < 1 {
		height = 1
	}
	avail := height - len(header) - len(footer)
	if avail < 0 {
		avail = 0
	}
	lines := make([]string, 0, height)
	lines = append(lines, header...)
	for i := 0; i < avail; i++ {
		if i < len(body) {
			lines = append(lines, body[i])
		} else {
			lines = append(lines, "")
		}
	}
	lines = append(lines, footer...)
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// scrollHint shows which way a list continues past the visible window, or an
// empty string when the whole list already fits.
func scrollHint(offset, visible, n int) string {
	if visible <= 0 || n <= visible {
		return ""
	}
	up, down := offset > 0, offset+visible < n
	switch {
	case up && down:
		return "↑↓"
	case up:
		return "↑"
	case down:
		return "↓"
	}
	return ""
}

// helpLine joins key hints with separators, dropping the hints at the end that
// do not fit. A narrow terminal loses the least important keys instead of
// wrapping the footer onto a second row.
func helpLine(width int, hints ...string) string {
	const sep = "  ·  "
	out := ""
	for _, h := range hints {
		next := h
		if out != "" {
			next = out + sep + h
		}
		if runeLen(next)+3 > width {
			break
		}
		out = next
	}
	if out == "" {
		return ""
	}
	return "  " + styleHelp.Render(out)
}
