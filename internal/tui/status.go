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

type StatusModel struct {
	status    *ipc.StatusData
	events    []ipc.EventLog
	err       error
	actionMsg string
	actionErr error
}

func NewStatusModel() StatusModel {
	return StatusModel{}
}

func (m StatusModel) Update(msg tea.Msg) (StatusModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "i":
			return m, installServiceCmd()
		case "u":
			return m, uninstallServiceCmd()
		}
	case statusMsg:
		m.status = msg.status
		m.events = msg.events
		m.err = msg.err
	case serviceActionMsg:
		m.actionErr = msg.err
		if msg.err == nil {
			m.actionMsg = msg.status
		} else {
			m.actionMsg = "Failed: " + msg.err.Error()
		}
	}
	return m, nil
}

func (m StatusModel) View() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("Daemon Status"))
	b.WriteString("\n")

	if m.err != nil {
		b.WriteString(styleMuted.Render(fmt.Sprintf("Error: %v", m.err)))
		b.WriteString("\n\n")
		b.WriteString(styleMuted.Render("Is the daemon running? Start with: poweraudio --daemon"))
		return b.String()
	}

	if m.status != nil {
		svcStatus := "not installed"
		if serviceFileExists() {
			if ServiceEnabled() {
				svcStatus = "enabled"
			} else {
				svcStatus = "installed (disabled)"
			}
		}

		b.WriteString(fmt.Sprintf("  Backend:  %s\n", styleActive.Render(m.status.Backend)))
		b.WriteString(fmt.Sprintf("  Socket:   %s\n", styleMuted.Render(m.status.Socket)))
		b.WriteString(fmt.Sprintf("  Uptime:   %s\n", formatUptime(m.status.UptimeSec)))
		b.WriteString(fmt.Sprintf("  Service:  %s\n", styleMuted.Render(svcStatus)))
	}

	if m.actionMsg != "" {
		b.WriteString("\n")
		if m.actionErr != nil {
			b.WriteString(styleMuted.Render("  " + m.actionMsg))
		} else {
			b.WriteString(styleActive.Render("  " + m.actionMsg))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styleSubtitle.Render("Recent Events"))
	b.WriteString("\n")

	if len(m.events) == 0 {
		b.WriteString(styleMuted.Render("  No events yet"))
		b.WriteString("\n")
	} else {
		start := 0
		if len(m.events) > 20 {
			start = len(m.events) - 20
		}
		for i := len(m.events) - 1; i >= start; i-- {
			ev := m.events[i]
			ts := ev.Time.Format("15:04:05")
			b.WriteString(fmt.Sprintf("  %s  %s\n", styleMuted.Render(ts), ev.Message))
		}
	}

	b.WriteString("\n")
	help := "[r] Refresh  [i] Install service  [u] Uninstall service"
	b.WriteString(styleHelp.Render(help))

	return b.String()
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
		bin, err := os.Executable()
		if err != nil {
			return serviceActionMsg{err: err}
		}
		bin, _ = filepath.EvalSymlinks(bin)

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

		return serviceActionMsg{status: "Service installed and enabled (will start on next login)"}
	}
}

func uninstallServiceCmd() tea.Cmd {
	return func() tea.Msg {
		exec.Command("systemctl", "--user", "disable", "poweraudio").Run()

		servicePath := filepath.Join(userConfigDir(), "systemd", "user", "poweraudio.service")
		os.Remove(servicePath)

		exec.Command("systemctl", "--user", "daemon-reload").Run()

		return serviceActionMsg{status: "Service uninstalled"}
	}
}
