package tui

import "charm.land/lipgloss/v2"

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

	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	styleSubtitle = lipgloss.NewStyle().Foreground(colorSecondary)
	styleAccent   = lipgloss.NewStyle().Foreground(colorPrimary)
	styleKey      = lipgloss.NewStyle().Foreground(colorSecondary)

	styleNormal   = lipgloss.NewStyle()
	styleSelected = lipgloss.NewStyle().Bold(true)
	styleMuted    = lipgloss.NewStyle().Foreground(colorMuted)
	styleActive   = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	styleWarn     = lipgloss.NewStyle().Foreground(colorWarning)
	styleError    = lipgloss.NewStyle().Foreground(colorError)

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
