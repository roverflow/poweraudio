package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/ipc"
)

// serviceCheckTTL is how stale the systemd answer is allowed to get. Asking
// systemctl is a subprocess, and View runs on every keystroke, so the state is
// cached and aged out rather than looked up while drawing.
const serviceCheckTTL = 10 * time.Second

type StatusModel struct {
	status    *ipc.StatusData
	events    []ipc.EventLog
	err       error
	actionMsg string
	actionErr error
	evOffset  int

	svcInstalled bool
	svcEnabled   bool
	svcCheckedAt time.Time

	width  int
	height int
}

func NewStatusModel() StatusModel {
	m := StatusModel{width: defaultWidth, height: defaultHeight - chromeLines}
	m.checkService(true)
	return m
}

// checkService refreshes the cached systemd state. Installing or removing the
// unit forces it; otherwise it only runs once the previous answer has aged out.
func (m *StatusModel) checkService(force bool) {
	if !force && time.Since(m.svcCheckedAt) < serviceCheckTTL {
		return
	}
	m.svcInstalled = serviceFileExists()
	m.svcEnabled = m.svcInstalled && ServiceEnabled()
	m.svcCheckedAt = time.Now()
}

func (m StatusModel) Update(msg tea.Msg) (StatusModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "i":
			return m, installServiceCmd()
		case "u":
			return m, uninstallServiceCmd()
		case "down", "j":
			m.evOffset = clampScroll(m.evOffset+1, len(m.events), m.eventRows())
		case "up", "k":
			m.evOffset = clampScroll(m.evOffset-1, len(m.events), m.eventRows())
		case "g", "home":
			m.evOffset = 0
		}
	case statusMsg:
		m.status = msg.status
		m.events = msg.events
		m.err = msg.err
		m.evOffset = clampScroll(m.evOffset, len(m.events), m.eventRows())
		m.checkService(false)
	case serviceActionMsg:
		m.actionErr = msg.err
		if msg.err == nil {
			m.actionMsg = msg.status
		} else {
			m.actionMsg = "Failed: " + msg.err.Error()
		}
		m.checkService(true)
	}
	return m, nil
}

func (m StatusModel) View() string {
	w := m.width
	if w < minWidth {
		w = minWidth
	}
	h := m.height
	if h < minContentH {
		h = minContentH
	}

	if m.err != nil {
		return frame(h,
			[]string{"  " + styleTitle.Render("Daemon Status"), ""},
			[]string{
				"  " + styleError.Render(truncate(m.err.Error(), w-2)),
				"",
				"  " + styleMuted.Render("Start it with: poweraudio --daemon"),
				"  " + styleMuted.Render("Or install the user service with i"),
			},
			[]string{"", helpLine(w, "r retry", "i install service", "? help")},
		)
	}

	visible := m.eventRows()

	header := []string{"  " + styleTitle.Render("Daemon Status"), ""}
	if m.status != nil {
		header = append(header,
			field("Backend", styleActive.Render(m.status.Backend)),
			field("Socket", styleMuted.Render(truncate(m.status.Socket, w-14))),
			field("Uptime", styleNormal.Render(formatUptime(m.status.UptimeSec))),
			field("Service", m.serviceState()),
		)
	} else {
		header = append(header, "", "", "", "")
	}

	if m.actionMsg != "" {
		line := styleActive.Render(truncate(m.actionMsg, w-2))
		if m.actionErr != nil {
			line = styleError.Render(truncate(m.actionMsg, w-2))
		}
		header = append(header, "", "  "+line)
	}

	events := "  " + styleSubtitle.Render("Recent Events")
	if hint := scrollHint(m.evOffset, visible, len(m.events)); hint != "" {
		events += styleMuted.Render(fmt.Sprintf("   %s  %d", hint, len(m.events)))
	}
	header = append(header, "", events, "")

	var body []string
	if len(m.events) == 0 {
		body = []string{"  " + styleMuted.Render("nothing logged yet")}
	} else {
		// Newest first, which is what you want when a switch just misfired.
		rows := make([]string, 0, len(m.events))
		for i := len(m.events) - 1; i >= 0; i-- {
			ev := m.events[i]
			stamp := styleMuted.Render(ev.Time.Format("15:04:05"))
			rows = append(rows, "  "+stamp+"  "+renderEvent(ev.Message, w-14))
		}
		body = window(rows, m.evOffset, visible)
	}

	footer := []string{
		"",
		helpLine(w, "j/k scroll", "r refresh", "i install service", "u remove", "? help"),
	}

	return frame(h, header, body, footer)
}

// eventRows mirrors the header and footer that View builds, so the scroll
// bounds match what actually fits.
func (m StatusModel) eventRows() int {
	used := 11
	if m.actionMsg != "" {
		used += 2
	}
	if n := m.height - used; n > 0 {
		return n
	}
	return 1
}

func field(label, value string) string {
	return "  " + styleMuted.Render(fit(label, 10)) + value
}

func (m StatusModel) serviceState() string {
	if !m.svcInstalled {
		return styleMuted.Render("not installed")
	}
	if m.svcEnabled {
		return styleActive.Render("enabled")
	}
	return styleWarn.Render("installed, disabled")
}

// renderEvent colours a log line by what it says happened. The daemon writes
// plain sentences, so matching on words is the only signal available.
func renderEvent(msg string, w int) string {
	text := truncate(msg, w)
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "fail"), strings.Contains(l, "error"), strings.Contains(l, "unavailable"):
		return styleError.Render(text)
	case strings.Contains(l, "switched"), strings.Contains(l, "fallback"):
		return styleActive.Render(text)
	case strings.Contains(l, "skipping"), strings.Contains(l, "giving up"), strings.Contains(l, "expired"):
		return styleWarn.Render(text)
	}
	return styleNormal.Render(text)
}

func formatUptime(seconds int) string {
	d := time.Duration(seconds) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := seconds % 60

	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

type statusMsg struct {
	status *ipc.StatusData
	events []ipc.EventLog
	err    error
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

func uninstallServiceCmd() tea.Cmd {
	return func() tea.Msg {
		exec.Command("systemctl", "--user", "disable", "poweraudio").Run()

		servicePath := filepath.Join(userConfigDir(), "systemd", "user", "poweraudio.service")
		os.Remove(servicePath)

		exec.Command("systemctl", "--user", "daemon-reload").Run()

		return serviceActionMsg{status: "Service removed"}
	}
}
