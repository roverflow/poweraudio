package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// The palette sticks to mid-tone colors that stay legible against both light
// and dark terminal backgrounds. Body text deliberately sets no foreground so
// it inherits whatever the terminal already uses; the previous near-white
// #F9FAFB rendered as white-on-white for anyone on a light theme.
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

	// styleSelectedRow fills the whole row rather than marking its text. A
	// bold word next to a thin bar was easy to lose on a busy screen, and the
	// bar alone disappeared entirely on terminals that ignore bold.
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

// rowPiece is one styled run of a list row. Rows are built from pieces rather
// than from pre-styled strings because the selected row swaps every piece onto
// the highlight background, and a background painted over text that already
// carries escape codes leaves unhighlighted gaps behind.
type rowPiece struct {
	text  string
	style lipgloss.Style

	// selFg keeps a piece's own colour on the highlight background. The green
	// marker on the default device survives selection that way.
	selFg color.Color
}

func piece(text string, style lipgloss.Style) rowPiece {
	return rowPiece{text: text, style: style}
}

func keepPiece(text string, style lipgloss.Style, fg color.Color) rowPiece {
	return rowPiece{text: text, style: style, selFg: fg}
}

// listRow renders one row of a list: a two column gutter, the pieces, and
// padding out to the terminal width less the final column. The last column is
// left empty because a row that fills the terminal exactly can trip auto-wrap
// and add a phantom line that pushes the whole frame off by one.
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
