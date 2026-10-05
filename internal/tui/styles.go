package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Mid-tone colors stay legible on light and dark terminals. Body text sets
// no foreground so it inherits the terminal's own.
var (
	colorPrimary   = lipgloss.Color("#7C3AED")
	colorSecondary = lipgloss.Color("#A78BFA")
	colorMuted     = lipgloss.Color("#6B7280")
	colorSuccess   = lipgloss.Color("#10B981")
	colorWarning   = lipgloss.Color("#F59E0B")
	colorError     = lipgloss.Color("#EF4444")
	colorOnPrimary = lipgloss.Color("#FFFFFF")

	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	styleSubtitle = lipgloss.NewStyle().Foreground(colorSecondary)
	styleAccent   = lipgloss.NewStyle().Foreground(colorPrimary)
	styleKey      = lipgloss.NewStyle().Foreground(colorSecondary)

	styleNormal = lipgloss.NewStyle()
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleActive = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	styleWarn   = lipgloss.NewStyle().Foreground(colorWarning)
	styleError  = lipgloss.NewStyle().Foreground(colorError)

	styleSelectedRow = lipgloss.NewStyle().
				Background(colorPrimary).
				Foreground(colorOnPrimary).
				Bold(true)

	styleTab = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(colorMuted)

	styleActiveTab = lipgloss.NewStyle().
			Padding(0, 2).
			Bold(true).
			Foreground(colorPrimary).
			Underline(true)

	styleStatusBar = lipgloss.NewStyle().Foreground(colorMuted)
	styleHelp      = lipgloss.NewStyle().Foreground(colorMuted)

	styleVolumeOn   = lipgloss.NewStyle().Foreground(colorSuccess)
	styleVolumeLoud = lipgloss.NewStyle().Foreground(colorWarning)
	styleVolumeOff  = lipgloss.NewStyle().Foreground(colorMuted)
)

// rowPiece is one styled run of a list row. Rows are built from pieces
// because painting a background over styled text leaves gaps.
type rowPiece struct {
	text  string
	style lipgloss.Style

	// selFg keeps this color on the highlight, like the default's green dot.
	selFg color.Color
}

func piece(text string, style lipgloss.Style) rowPiece {
	return rowPiece{text: text, style: style}
}

func keepPiece(text string, style lipgloss.Style, fg color.Color) rowPiece {
	return rowPiece{text: text, style: style, selFg: fg}
}

// listRow leaves the last column empty. A row that fills the terminal can
// trip auto-wrap and push the frame down a line.
func listRow(width int, selected bool, pieces ...rowPiece) string {
	band := width - 3
	if band < 1 {
		band = 1
	}

	plain := 0
	for _, p := range pieces {
		plain += runeLen(p.text)
	}

	var b strings.Builder
	b.WriteString("  ")
	for _, p := range pieces {
		if !selected {
			b.WriteString(p.style.Render(p.text))
			continue
		}
		st := styleSelectedRow
		if p.selFg != nil {
			st = st.Foreground(p.selFg)
		}
		b.WriteString(st.Render(p.text))
	}
	if pad := band - plain; pad > 0 {
		fill := strings.Repeat(" ", pad)
		if selected {
			fill = styleSelectedRow.Render(fill)
		}
		b.WriteString(fill)
	}
	return b.String()
}
