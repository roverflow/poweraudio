package tui

import "strings"

const (
	// Sizes used before the first resize message arrives.
	defaultWidth  = 80
	defaultHeight = 24

	// chromeLines is the tab bar, two blank lines and the status bar.
	chromeLines = 4
)

// screenSize floors the size at one cell. Screens get the real terminal
// size, not a minimum, since rows wider than the terminal corrupt the frame.
func screenSize(width, height int) (int, int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return width, height
}

func runeLen(s string) int {
	return len([]rune(s))
}

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

func padRight(s string, n int) string {
	if d := n - runeLen(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func fit(s string, n int) string {
	return padRight(truncate(s, n), n)
}

// clampOffset scrolls the least distance that keeps cursor visible.
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

// frame pads or clips body so the output is exactly height lines.
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

// helpLine drops trailing hints that do not fit instead of wrapping.
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
