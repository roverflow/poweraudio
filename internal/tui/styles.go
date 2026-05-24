package tui

import "github.com/charmbracelet/lipgloss/v2"

var (
	colorPrimary   = lipgloss.Color("#7C3AED")
	colorSecondary = lipgloss.Color("#A78BFA")
	colorMuted     = lipgloss.Color("#6B7280")
	colorSuccess   = lipgloss.Color("#10B981")
	colorWarning   = lipgloss.Color("#F59E0B")
	colorError     = lipgloss.Color("#EF4444")
	colorBg        = lipgloss.Color("#1F2937")
	colorFg        = lipgloss.Color("#F9FAFB")

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginBottom(1)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(colorSecondary).
			MarginBottom(1)

	styleSelected = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	styleNormal = lipgloss.NewStyle().
			Foreground(colorFg)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleActive = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	styleTab = lipgloss.NewStyle().
			Padding(0, 2)

	styleActiveTab = lipgloss.NewStyle().
			Padding(0, 2).
			Bold(true).
			Foreground(colorPrimary).
			Underline(true)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(colorMuted).
			MarginTop(1)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleVolumeOn  = lipgloss.NewStyle().Foreground(colorSuccess)
	styleVolumeOff = lipgloss.NewStyle().Foreground(colorMuted)
)
