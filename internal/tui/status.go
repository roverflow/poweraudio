package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/roverflow/poweraudio/internal/ipc"
)

// serviceCheckTTL caches systemctl answers, since View runs per keystroke.
const serviceCheckTTL = 10 * time.Second

const (
	// statusFullHeader is the title, a blank, four fields, a blank, the log
	// heading and a blank.
	statusFullHeader = 9

	// statusTightHeader is the title and a blank.
	statusTightHeader = 2

	// statusStampFormat carries the date because the log spans days.
	statusStampFormat = "Jan 02 15:04:05"
)

type statusModel struct {
	status ipc.StatusData
	events []ipc.EventLog
	ready  bool

	evOffset int

	svcInstalled bool
	svcEnabled   bool
	svcCheckedAt time.Time

	width  int
	height int
}

func newStatusModel() statusModel {
	m := statusModel{width: defaultWidth, height: defaultHeight - chromeLines}
	m.checkService(true)
	return m
}

func (m *statusModel) checkService(force bool) {
	if !force && time.Since(m.svcCheckedAt) < serviceCheckTTL {
		return
	}
	m.svcInstalled = serviceFileExists()
	m.svcEnabled = m.svcInstalled && serviceEnabled()
	m.svcCheckedAt = time.Now()
}

func (m statusModel) Update(msg tea.Msg) (statusModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "i":
			if !m.svcInstalled {
				return m, installServiceCmd()
			}
		case "u":
			if m.svcInstalled {
				return m, removeServiceCmd()
			}
		case "down", "j":
			m.evOffset = clampScroll(m.evOffset+1, len(m.events), m.eventRows())
		case "up", "k":
			m.evOffset = clampScroll(m.evOffset-1, len(m.events), m.eventRows())
		case "g", "home":
			m.evOffset = 0
		}
	}
	return m, nil
}

func (m *statusModel) setSnapshot(snap ipc.Snapshot) {
	m.status = snap.Status
	m.events = snap.Events
	m.ready = true
	m.evOffset = clampScroll(m.evOffset, len(m.events), m.eventRows())
	m.checkService(false)
}

func (m statusModel) scroll(delta int) (statusModel, tea.Cmd) {
	m.evOffset = clampScroll(m.evOffset+delta, len(m.events), m.eventRows())
	return m, nil
}

func (m statusModel) View() string {
	w, h := screenSize(m.width, m.height)

	if !m.ready {
		return frame(h,
			[]string{"  " + styleTitle.Render("Daemon Status"), ""},
			[]string{
				"  " + styleMuted.Render("The daemon is not answering"),
				"",
				"  " + styleMuted.Render("Start it with: poweraudio --daemon"),
				"  " + styleMuted.Render("Or install the user service with i"),
			},
			[]string{"", helpLine(w, "r reconnect", "i install service", "? help", "q quit")},
		)
	}

	visible := m.eventRows()
	fields := statusShowsFields(h)

	title := "  " + styleTitle.Render("Daemon Status")
	if v := m.status.Version; v != "" {
		title += styleMuted.Render("  " + v)
	}
	if !fields {
		title += styleMuted.Render("   " + m.audioLine())
	}
	if hint := scrollHint(m.evOffset, visible, len(m.events)); hint != "" {
		title += styleMuted.Render(fmt.Sprintf("   %s  %d", hint, len(m.events)))
	}

	header := []string{title, ""}
	if fields {
		header = append(header,
			field("Audio", styleActive.Render(truncate(m.audioLine(), w-14))),
			field("Config", styleMuted.Render(truncate(m.status.ConfigPath, w-14))),
			field("Uptime", styleNormal.Render(m.uptime())),
			field("Service", m.serviceState()),
			"",
			"  "+styleSubtitle.Render("Recent Events"),
			"",
		)
	}

	var body []string
	if len(m.events) == 0 {
		body = []string{"  " + styleMuted.Render("nothing logged yet")}
	} else {
		rows := make([]string, 0, len(m.events))
		for i := len(m.events) - 1; i >= 0; i-- {
			rows = append(rows, eventRow(m.events[i], w))
		}
		body = window(rows, m.evOffset, visible)
	}

	footer := []string{helpLine(w, m.helpHints()...)}
	if fields {
		footer = append([]string{""}, footer...)
	}

	return frame(h, header, body, footer)
}

func (m statusModel) helpHints() []string {
	hints := []string{"j/k scroll", "r refresh"}
	if m.svcInstalled {
		hints = append(hints, "u remove service")
	} else {
		hints = append(hints, "i install service")
	}
	return append(hints, "? help", "q quit")
}

func statusShowsFields(height int) bool {
	return height-statusFullHeader-2 >= 2
}

// eventRows must mirror the header and footer that View builds.
func (m statusModel) eventRows() int {
	_, h := screenSize(m.width, m.height)
	used := statusTightHeader + 1
	if statusShowsFields(h) {
		used = statusFullHeader + 2
	}
	if n := h - used; n > 0 {
		return n
	}
	return 1
}

// audioLine falls back to the backend name until the first probe lands.
func (m statusModel) audioLine() string {
	if m.status.Audio.ProbedAt.IsZero() {
		return m.status.Backend
	}
	line := m.status.Audio.Summary()
	if m.status.Switching != "" {
		line += " · " + m.status.Switching
	}
	return line
}

func (m statusModel) uptime() string {
	if m.status.StartedAt.IsZero() {
		return "unknown"
	}
	return formatUptime(time.Since(m.status.StartedAt))
}

func (m statusModel) serviceState() string {
	if !m.svcInstalled {
		return styleMuted.Render("not installed")
	}
	if m.svcEnabled {
		return styleActive.Render("enabled")
	}
	return styleWarn.Render("installed, disabled")
}

func eventRow(ev ipc.EventLog, width int) string {
	stamp := styleMuted.Render(ev.Time.Format(statusStampFormat))
	text := truncate(ev.Message, width-21)
	return "  " + stamp + "  " + eventStyle(ev.Level).Render(text)
}

func eventStyle(level ipc.Level) lipgloss.Style {
	switch level {
	case ipc.LevelDebug:
		return styleMuted
	case ipc.LevelWarn:
		return styleWarn
	case ipc.LevelError:
		return styleError
	default:
		return styleNormal
	}
}

func formatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

type serviceActionMsg struct {
	status string
	err    error
}

func installServiceCmd() tea.Cmd {
	return func() tea.Msg {
		bin, err := resolvedExecutable()
		if err != nil {
			return serviceActionMsg{err: err}
		}

		serviceDir := filepath.Join(userConfigDir(), "systemd", "user")
		servicePath := filepath.Join(serviceDir, "poweraudio.service")

		if err := os.MkdirAll(serviceDir, 0o755); err != nil {
			return serviceActionMsg{err: fmt.Errorf("creating dir: %w", err)}
		}

		content := generateServiceFile(bin)
		if err := os.WriteFile(servicePath, []byte(content), 0o644); err != nil {
			return serviceActionMsg{err: fmt.Errorf("writing service: %w", err)}
		}

		if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return serviceActionMsg{err: fmt.Errorf("daemon-reload: %s: %w", strings.TrimSpace(string(out)), err)}
		}

		if out, err := exec.Command("systemctl", "--user", "enable", "poweraudio").CombinedOutput(); err != nil {
			return serviceActionMsg{err: fmt.Errorf("enable: %s: %w", strings.TrimSpace(string(out)), err)}
		}

		return serviceActionMsg{status: "Service installed and enabled, starts on next login"}
	}
}

// removeServiceCmd uses --now so the running daemon stops too.
func removeServiceCmd() tea.Cmd {
	return func() tea.Msg {
		exec.Command("systemctl", "--user", "disable", "--now", "poweraudio").Run()

		servicePath := filepath.Join(userConfigDir(), "systemd", "user", "poweraudio.service")
		os.Remove(servicePath)

		exec.Command("systemctl", "--user", "daemon-reload").Run()

		return serviceActionMsg{status: "Service stopped and removed"}
	}
}
