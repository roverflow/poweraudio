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

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type SetupModel struct {
	client     *ipc.Client
	cursor     int
	status     string
	err        error
	starting   bool
	done       bool
	frame      int
	binaryPath string

	serviceInstalled bool
}

func NewSetupModel(client *ipc.Client) SetupModel {
	bin, _ := os.Executable()
	bin, _ = filepath.EvalSymlinks(bin)

	return SetupModel{
		client:           client,
		binaryPath:       bin,
		serviceInstalled: serviceFileExists(),
	}
}

func (m SetupModel) Init() tea.Cmd {
	return nil
}

func (m SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		// While a daemon is coming up, swallow everything. This guard used
		// to sit behind a flag that was set on a discarded copy of the
		// model, so a second Enter would launch a second daemon that then
		// stole the first one's socket.
		if m.starting {
			return m, nil
		}

		switch key {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < 2 {
				m.cursor++
			}
		case "q":
			return m, tea.Quit
		case "enter":
			switch setupAction(m.cursor) {
			case actionQuit:
				return m, tea.Quit
			case actionStartProcess:
				m.starting = true
				m.err = nil
				m.status = "Starting the daemon"
				return m, tea.Batch(m.startProcess(), spinCmd())
			case actionInstallService:
				m.starting = true
				m.err = nil
				m.status = "Installing the user service"
				return m, tea.Batch(m.installService(), spinCmd())
			}
		}

	case spinTickMsg:
		if !m.starting {
			return m, nil
		}
		m.frame++
		return m, spinCmd()

	case setupResultMsg:
		m.starting = false
		if msg.err != nil {
			m.err = msg.err
			m.status = ""
			return m, nil
		}
		m.status = msg.status
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m SetupModel) View() tea.View {
	var b strings.Builder

	b.WriteString("  " + styleTitle.Render("poweraudio"))
	b.WriteString("\n")
	b.WriteString("  " + styleMuted.Render("The audio daemon is not running yet"))
	b.WriteString("\n\n")

	if m.starting {
		spin := styleAccent.Render(spinFrames[m.frame%len(spinFrames)])
		b.WriteString("  " + spin + " " + styleSubtitle.Render(m.status+"…"))
		b.WriteString("\n")
		return setupView(b.String())
	}

	if m.done {
		b.WriteString("  " + styleActive.Render("✓ "+m.status))
		b.WriteString("\n\n")
		b.WriteString("  " + styleMuted.Render("Opening the interface"))
		return setupView(b.String())
	}

	if m.serviceInstalled {
		b.WriteString("  " + styleMuted.Render("A service file is already installed"))
		b.WriteString("\n\n")
	}

	if m.err != nil {
		b.WriteString("  " + styleError.Render(truncate(m.err.Error(), 76)))
		b.WriteString("\n\n")
	}

	options := []struct{ label, desc string }{
		{"Start the daemon now", "Runs until you log out"},
		{"Install the user service", "Starts on login and restarts after a crash"},
		{"Quit", ""},
	}

	for i, opt := range options {
		prefix := "  "
		label := styleNormal.Render(opt.label)
		if i == m.cursor {
			prefix = styleAccent.Render("▎") + " "
			label = styleSelected.Render(opt.label)
		}
		b.WriteString(prefix + label)
		b.WriteString("\n")
		if opt.desc != "" {
			b.WriteString("    " + styleMuted.Render(opt.desc))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpLine(80, "enter select", "j/k move", "q quit"))

	return setupView(b.String())
}

func setupView(s string) tea.View {
	v := tea.NewView(s)
	v.AltScreen = true
	return v
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
		return setupResultMsg{err: fmt.Errorf("daemon started but never answered on the socket")}
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
			return setupResultMsg{err: fmt.Errorf("daemon-reload: %s: %w", strings.TrimSpace(string(out)), err)}
		}

		if out, err := exec.Command("systemctl", "--user", "enable", "--now", "poweraudio").CombinedOutput(); err != nil {
			return setupResultMsg{err: fmt.Errorf("enable service: %s: %w", strings.TrimSpace(string(out)), err)}
		}

		for i := 0; i < 30; i++ {
			time.Sleep(100 * time.Millisecond)
			if m.client.Ping() {
				return setupResultMsg{status: "Service installed and started"}
			}
		}
		return setupResultMsg{status: "Service installed, the daemon may still be starting"}
	}
}

func (m SetupModel) Done() bool {
	return m.done
}

type setupResultMsg struct {
	status string
	err    error
}

type spinTickMsg struct{}

func spinCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return spinTickMsg{}
	})
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
