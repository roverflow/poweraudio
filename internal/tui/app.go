package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
)

type Screen int

const (
	ScreenDevices Screen = iota
	ScreenPriorities
	ScreenStatus
)

type Model struct {
	screen     Screen
	devices    DevicesModel
	priorities PrioritiesModel
	status     StatusModel
	client     *ipc.Client
	width      int
	height     int
	connected  bool
}

func NewModel(client *ipc.Client) Model {
	return Model{
		screen:     ScreenDevices,
		devices:    NewDevicesModel(),
		priorities: NewPrioritiesModel(),
		status:     NewStatusModel(),
		client:     client,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshAll(),
		m.tickCmd(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "d", "1":
			m.screen = ScreenDevices
			return m, nil
		case "p", "2":
			m.screen = ScreenPriorities
			return m, nil
		case "s", "3":
			m.screen = ScreenStatus
			return m, nil
		case "r":
			return m, m.refreshAll()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.devices.width = msg.Width
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.refreshAll(), m.tickCmd())

	case switchDeviceMsg:
		return m, m.switchDevice(msg.deviceID)

	case setDefaultMsg:
		return m, m.refreshAll()

	case volumeMsg:
		return m, m.setVolume(msg.deviceID, msg.percent)

	case muteMsg:
		return m, m.toggleMute(msg.deviceID)

	case volumeResultMsg, muteResultMsg:
		return m, m.refreshAll()

	case savePrioritiesMsg:
		return m, m.savePriorities(msg.priorities)

	case saveSwitchingMsg:
		return m, m.saveSwitching(msg.switching)

	case refreshResultMsg:
		m.connected = msg.connected
		m.devices, _ = m.devices.Update(msg.devices)
		m.priorities, _ = m.priorities.Update(msg.priorities)
		m.status, _ = m.status.Update(msg.status)
		return m, nil
	}

	var cmd tea.Cmd
	switch m.screen {
	case ScreenDevices:
		m.devices, cmd = m.devices.Update(msg)
	case ScreenPriorities:
		m.priorities, cmd = m.priorities.Update(msg)
	case ScreenStatus:
		m.status, cmd = m.status.Update(msg)
	}
	return m, cmd
}

func (m Model) View() tea.View {
	var b strings.Builder

	b.WriteString(m.renderTabs())
	b.WriteString("\n\n")

	switch m.screen {
	case ScreenDevices:
		b.WriteString(m.devices.View())
	case ScreenPriorities:
		b.WriteString(m.priorities.View())
	case ScreenStatus:
		b.WriteString(m.status.View())
	}

	b.WriteString("\n\n")
	b.WriteString(m.renderStatusBar())

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m Model) renderTabs() string {
	tabs := []struct {
		name   string
		key    string
		screen Screen
	}{
		{"Devices", "d", ScreenDevices},
		{"Config", "p", ScreenPriorities},
		{"Status", "s", ScreenStatus},
	}

	var parts []string
	for _, t := range tabs {
		label := fmt.Sprintf("[%s] %s", t.key, t.name)
		if t.screen == m.screen {
			parts = append(parts, styleActiveTab.Render(label))
		} else {
			parts = append(parts, styleTab.Render(label))
		}
	}

	return strings.Join(parts, "  ")
}

func (m Model) renderStatusBar() string {
	conn := "Connected"
	if !m.connected {
		conn = "Disconnected"
	}
	return styleStatusBar.Render(fmt.Sprintf("poweraudio  │  Daemon: %s  │  [q] Quit", conn))
}

type refreshResultMsg struct {
	connected  bool
	devices    devicesMsg
	status     statusMsg
	priorities prioritiesMsg
}

func (m Model) refreshAll() tea.Cmd {
	return func() tea.Msg {
		var result refreshResultMsg

		devices, devErr := m.client.ListDevices()
		if devErr != nil {
			result.devices = devicesMsg{err: devErr}
			result.status = statusMsg{err: devErr}
			return result
		}

		result.connected = true
		result.devices = devicesMsg{devices: devices}

		status, _ := m.client.GetStatus()
		events, _ := m.client.GetEvents(50)
		result.status = statusMsg{status: status, events: events}

		cfg, _ := m.client.GetConfig()
		if cfg != nil {
			result.priorities = prioritiesMsg{
				priorities: cfg.Priority,
				devices:    devices,
				switching:  cfg.Switching,
			}
		} else {
			result.priorities = prioritiesMsg{devices: devices}
		}

		return result
	}
}

func (m Model) switchDevice(deviceID string) tea.Cmd {
	return func() tea.Msg {
		err := m.client.SetDefault(deviceID)
		return setDefaultMsg{err: err}
	}
}

func (m Model) savePriorities(priorities []config.PriorityEntry) tea.Cmd {
	return func() tea.Msg {
		err := m.client.UpdatePriorities(priorities)
		return savePrioritiesResultMsg{err: err}
	}
}

func (m Model) saveSwitching(switching config.SwitchingConfig) tea.Cmd {
	return func() tea.Msg {
		err := m.client.UpdateSwitching(switching.OnConnect, switching.OnDisconnect)
		return saveSwitchingResultMsg{err: err}
	}
}

func (m Model) setVolume(deviceID string, percent int) tea.Cmd {
	return func() tea.Msg {
		err := m.client.SetVolume(deviceID, percent)
		return volumeResultMsg{err: err}
	}
}

func (m Model) toggleMute(deviceID string) tea.Cmd {
	return func() tea.Msg {
		err := m.client.ToggleMute(deviceID)
		return muteResultMsg{err: err}
	}
}

type tickMsg struct{}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}
