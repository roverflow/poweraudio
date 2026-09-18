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

// serviceCheckTTL is how stale the systemd answer is allowed to get. Asking
// systemctl is a subprocess, and View runs on every keystroke, so the state is
// cached and aged out rather than looked up while drawing.
const serviceCheckTTL = 10 * time.Second

const (
	// statusFullHeader is the title, a blank, four fields, a blank, the log
	// heading and a blank.
	statusFullHeader = 9

	// statusTightHeader is the title and a blank, which is what is left when
	// the terminal is too short for the fields.
	statusTightHeader = 2

	// statusStampFormat carries the date because the log spans days, and a
	// bare clock made yesterday's failure look like this morning's.
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

// checkService refreshes the cached systemd state. Installing or removing the
// unit forces it; otherwise it only runs once the previous answer has aged out.
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
	if !fields {
		title += styleMuted.Render("   " + m.status.Backend)
	}
	if hint := scrollHint(m.evOffset, visible, len(m.events)); hint != "" {
		title += styleMuted.Render(fmt.Sprintf("   %s  %d", hint, len(m.events)))
	}

	header := []string{title, ""}
	if fields {
		header = append(header,
			field("Backend", styleActive.Render(truncate(m.status.Backend, w-14))),
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
		// Newest first, which is what you want when a switch just misfired.
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

// helpHints offers the service key that applies. Offering both used to tell
// someone with no unit file that they could remove it.
func (m statusModel) helpHints() []string {
	hints := []string{"j/k scroll", "r refresh"}
	if m.svcInstalled {
		hints = append(hints, "u remove service")
	} else {
		hints = append(hints, "i install service")
	}
	return append(hints, "? help", "q quit")
}

// statusShowsFields drops the fields on a short terminal. The fixed header
// used to eat the whole budget at fourteen rows, and frame then clipped the
// log and the help line off the bottom.
func statusShowsFields(height int) bool {
	return height-statusFullHeader-2 >= 2
}

// eventRows mirrors the header and footer that View builds, so the scroll
// bounds match what actually fits.
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

// eventRow stamps a log line with its date and colours it by the level the
// daemon recorded. Matching keywords in the sentence used to call a successful
// "switch failed over" line an error.
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

// removeServiceCmd stops the running unit as well as disabling it. Leaving out
// --now disabled the unit for the next login and left the daemon running, so
// the UI reported the service gone while it was still switching sinks.
func removeServiceCmd() tea.Cmd {
	return func() tea.Msg {
		exec.Command("systemctl", "--user", "disable", "--now", "poweraudio").Run()

		servicePath := filepath.Join(userConfigDir(), "systemd", "user", "poweraudio.service")
		os.Remove(servicePath)

		exec.Command("systemctl", "--user", "daemon-reload").Run()

		return serviceActionMsg{status: "Service stopped and removed"}
	}
}
