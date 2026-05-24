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

type setupAction int

const (
	actionStartProcess setupAction = iota
	actionInstallService
	actionQuit
)

type SetupModel struct {
	client     *ipc.Client
	cursor     int
	status     string
	err        error
	starting   bool
	done       bool
	binaryPath string

	serviceInstalled bool
	serviceRunning   bool
}

func NewSetupModel(client *ipc.Client) SetupModel {
	bin, _ := os.Executable()
	bin, _ = filepath.EvalSymlinks(bin)

	installed := serviceFileExists()
	running := client.Ping()

	return SetupModel{
		client:           client,
		binaryPath:       bin,
		serviceInstalled: installed,
		serviceRunning:   running,
	}
}

func (m SetupModel) Init() tea.Cmd {
	return nil
}

func (m SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.starting {
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < 2 {
				m.cursor++
			}
		case "enter":
			return m, m.executeAction(setupAction(m.cursor))
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case setupResultMsg:
		m.starting = false
		if msg.err != nil {
			m.err = msg.err
			m.status = "Failed: " + msg.err.Error()
		} else {
			m.status = msg.status
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m SetupModel) View() tea.View {
	var b strings.Builder

	b.WriteString(styleTitle.Render("poweraudio"))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("Audio output controller daemon is not running"))
	b.WriteString("\n\n")

	if m.serviceInstalled {
		b.WriteString(styleMuted.Render("  Service file: installed"))
		b.WriteString("\n\n")
	}

	if m.starting {
		b.WriteString(styleSubtitle.Render("  Starting daemon..."))
		b.WriteString("\n")
		v := tea.NewView(b.String())
		v.AltScreen = true
		return v
	}

	if m.done {
		b.WriteString(styleActive.Render("  " + m.status))
		b.WriteString("\n\n")
		b.WriteString(styleMuted.Render("  Launching TUI..."))
		v := tea.NewView(b.String())
		v.AltScreen = true
		return v
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(styleMuted.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n\n")
	}

	options := []struct {
		label string
		desc  string
	}{
		{"Start daemon (background process)", "Run once — stops when you log out"},
		{"Install as systemd service (recommended)", "Auto-starts on login, restarts on crash"},
		{"Quit", ""},
	}

	for i, opt := range options {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		line := cursor + opt.label
		if i == m.cursor {
			b.WriteString(styleSelected.Render(line))
		} else {
			b.WriteString(styleNormal.Render(line))
		}
		b.WriteString("\n")
		if opt.desc != "" {
			b.WriteString(styleMuted.Render("    " + opt.desc))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(styleHelp.Render("[Enter] Select  [q] Quit"))

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m SetupModel) executeAction(action setupAction) tea.Cmd {
	switch action {
	case actionStartProcess:
		m.starting = true
		return m.startProcess()
	case actionInstallService:
		m.starting = true
		return m.installService()
	case actionQuit:
		return tea.Quit
	}
	return nil
}

func (m SetupModel) startProcess() tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command(m.binaryPath, "--daemon")
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.Stdin = nil
		attr := syscallProcAttr()
		cmd.SysProcAttr = &attr

		if err := cmd.Start(); err != nil {
			return setupResultMsg{err: fmt.Errorf("starting daemon: %w", err)}
		}

		cmd.Process.Release()

		for i := 0; i < 20; i++ {
			time.Sleep(100 * time.Millisecond)
			if m.client.Ping() {
				return setupResultMsg{status: "Daemon started"}
			}
		}
		return setupResultMsg{err: fmt.Errorf("daemon started but not responding")}
	}
}

func (m SetupModel) installService() tea.Cmd {
	return func() tea.Msg {
		serviceDir := filepath.Join(userConfigDir(), "systemd", "user")
		servicePath := filepath.Join(serviceDir, "poweraudio.service")

		if err := os.MkdirAll(serviceDir, 0o755); err != nil {
			return setupResultMsg{err: fmt.Errorf("creating service dir: %w", err)}
		}

		content := generateServiceFile(m.binaryPath)
		if err := os.WriteFile(servicePath, []byte(content), 0o644); err != nil {
			return setupResultMsg{err: fmt.Errorf("writing service file: %w", err)}
		}

		if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return setupResultMsg{err: fmt.Errorf("daemon-reload: %s: %w", string(out), err)}
		}

		if out, err := exec.Command("systemctl", "--user", "enable", "--now", "poweraudio").CombinedOutput(); err != nil {
			return setupResultMsg{err: fmt.Errorf("enable service: %s: %w", string(out), err)}
		}

		for i := 0; i < 30; i++ {
			time.Sleep(100 * time.Millisecond)
			if m.client.Ping() {
				return setupResultMsg{status: "Service installed and started"}
			}
		}
		return setupResultMsg{status: "Service installed — daemon may still be starting"}
	}
}

func (m SetupModel) Done() bool {
	return m.done
}

type setupResultMsg struct {
	status string
	err    error
}

func generateServiceFile(binaryPath string) string {
	return fmt.Sprintf(`[Unit]
Description=poweraudio - Audio output controller daemon
After=pipewire.service wireplumber.service
Wants=pipewire.service

[Service]
Type=simple
ExecStart=%s --daemon
Restart=on-failure
RestartSec=5
Environment=XDG_RUNTIME_DIR=%%t

[Install]
WantedBy=default.target
`, binaryPath)
}

func serviceFileExists() bool {
	path := filepath.Join(userConfigDir(), "systemd", "user", "poweraudio.service")
	_, err := os.Stat(path)
	return err == nil
}

func ServiceEnabled() bool {
	out, err := exec.Command("systemctl", "--user", "is-enabled", "poweraudio").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "enabled"
}

func userConfigDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}
