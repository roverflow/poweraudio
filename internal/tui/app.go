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
	ScreenHelp
)

type noticeKind int

const (
	noticeInfo noticeKind = iota
	noticeWarn
	noticeErr
)

// noticeTTL is how long an action result stays in the status bar before the
// refresh tick clears it.
const noticeTTL = 5 * time.Second

type notice struct {
	text string
	kind noticeKind
	at   time.Time
}

type Model struct {
	screen     Screen
	prevScreen Screen
	devices    DevicesModel
	priorities PrioritiesModel
	status     StatusModel
	client     *ipc.Client
	width      int
	height     int
	connected  bool

	note        notice
	confirmQuit bool
}

func NewModel(client *ipc.Client) Model {
	m := Model{
		screen:     ScreenDevices,
		prevScreen: ScreenDevices,
		devices:    NewDevicesModel(),
		priorities: NewPrioritiesModel(),
		status:     NewStatusModel(),
		client:     client,
		width:      defaultWidth,
		height:     defaultHeight,
	}
	m.applySize()
	return m
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
		key := msg.String()
		if key != "q" {
			m.confirmQuit = false
		}

		switch key {
		case "ctrl+c":
			return m, tea.Quit

		case "q":
			// Reordering a priority list and forgetting to press w used to
			// throw the work away without a word. Ask once.
			if m.priorities.hasUnsaved() && !m.confirmQuit {
				m.confirmQuit = true
				m.setNotice(noticeWarn, "Unsaved config. q again to discard, w to save.")
				return m, nil
			}
			return m, tea.Quit

		case "?":
			if m.screen == ScreenHelp {
				m.screen = m.prevScreen
			} else {
				m.prevScreen = m.screen
				m.screen = ScreenHelp
			}
			return m, nil

		case "esc":
			if m.screen == ScreenHelp {
				m.screen = m.prevScreen
				return m, nil
			}

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

		if m.screen == ScreenHelp {
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.applySize()
		return m, nil

	case tickMsg:
		if !m.note.at.IsZero() && time.Since(m.note.at) > noticeTTL {
			m.note = notice{}
		}
		return m, tea.Batch(m.refreshAll(), m.tickCmd())

	case switchDeviceMsg:
		return m, m.switchDevice(msg.deviceID)

	case setDefaultMsg:
		if msg.err != nil {
			m.setNotice(noticeErr, "Set default failed: "+msg.err.Error())
		} else {
			m.setNotice(noticeInfo, "Default output changed")
		}
		return m, m.refreshAll()

	case volumeMsg:
		return m, m.setVolume(msg.deviceID, msg.percent)

	case muteMsg:
		return m, m.toggleMute(msg.deviceID)

	case volumeResultMsg:
		if msg.err != nil {
			m.setNotice(noticeErr, "Volume change failed: "+msg.err.Error())
		}
		return m, m.refreshAll()

	case muteResultMsg:
		if msg.err != nil {
			m.setNotice(noticeErr, "Mute toggle failed: "+msg.err.Error())
		}
		return m, m.refreshAll()

	case savePrioritiesMsg:
		return m, m.savePriorities(msg.priorities)

	case saveSwitchingMsg:
		return m, m.saveSwitching(msg.switching)

	case savePrioritiesResultMsg:
		if msg.err != nil {
			m.setNotice(noticeErr, "Save failed: "+msg.err.Error())
		} else {
			m.setNotice(noticeInfo, "Priorities saved")
		}
		m.priorities, _ = m.priorities.Update(msg)
		return m, nil

	case saveSwitchingResultMsg:
		if msg.err != nil {
			m.setNotice(noticeErr, "Save failed: "+msg.err.Error())
		} else {
			m.setNotice(noticeInfo, "Switching rules saved")
		}
		m.priorities, _ = m.priorities.Update(msg)
		return m, nil

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
	w, h := m.contentSize()

	var content string
	switch m.screen {
	case ScreenDevices:
		content = m.devices.View()
	case ScreenPriorities:
		content = m.priorities.View()
	case ScreenStatus:
		content = m.status.View()
	case ScreenHelp:
		content = helpScreen(w, h)
	}

	// Exactly h lines of content, so the whole frame comes to h+chromeLines
	// and the status bar always lands on the bottom row.
	body := strings.Join([]string{
		m.renderTabs(),
		"",
		content,
		"",
		m.renderStatusBar(w),
	}, "\n")

	v := tea.NewView(body)
	v.AltScreen = true
	return v
}

// contentSize is the space left for the active screen once the tab bar, the
// blank lines and the status bar have taken their rows.
func (m Model) contentSize() (int, int) {
	w := m.width
	if w < minWidth {
		w = minWidth
	}
	h := m.height - chromeLines
	if h < minContentH {
		h = minContentH
	}
	return w, h
}

func (m *Model) applySize() {
	w, h := m.contentSize()

	m.devices.width, m.devices.height = w, h
	m.priorities.width, m.priorities.height = w, h
	m.status.width, m.status.height = w, h

	m.devices.syncScroll()
	m.priorities.syncScroll()
}

func (m *Model) setNotice(kind noticeKind, text string) {
	m.note = notice{text: text, kind: kind, at: time.Now()}
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

	parts := make([]string, 0, len(tabs))
	for _, t := range tabs {
		label := fmt.Sprintf("%s %s", t.key, t.name)
		if t.screen == m.screen {
			parts = append(parts, styleActiveTab.Render(label))
		} else {
			parts = append(parts, styleTab.Render(label))
		}
	}
	return strings.Join(parts, "")
}

// renderStatusBar keeps the daemon state on the left and the most recent action
// result on the right, padded so the two ends sit against the terminal edges.
func (m Model) renderStatusBar(width int) string {
	state, dot := "daemon offline", styleError.Render("●")
	if m.connected {
		state, dot = "daemon ready", styleActive.Render("●")
	}

	leftPlain := "  ● " + state
	left := "  " + dot + styleStatusBar.Render(" "+state)

	rightPlain := "? help  ·  q quit  "
	right := styleStatusBar.Render(rightPlain)

	if m.note.text != "" {
		text := truncate(m.note.text, width-runeLen(leftPlain)-4)
		rightPlain = text + "  "
		switch m.note.kind {
		case noticeErr:
			right = styleError.Render(text) + "  "
		case noticeWarn:
			right = styleWarn.Render(text) + "  "
		default:
			right = styleActive.Render(text) + "  "
		}
	}

	gap := width - runeLen(leftPlain) - runeLen(rightPlain) - 1
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// helpScreen is a static key reference. It does not scroll, so it is kept
// short enough to fit a 24 row terminal.
func helpScreen(width, height int) string {
	rows := []struct{ key, desc string }{
		{"d p s", "devices, config, status"},
		{"? r q", "help, refresh, quit"},
		{"", ""},
		{"Devices", ""},
		{"j k", "move, also g G pgup pgdn"},
		{"enter", "make the selected device the default output"},
		{"h l", "volume down and up in 5% steps"},
		{"m", "mute or unmute"},
		{"", ""},
		{"Config", ""},
		{"tab", "swap between priorities and switching rules"},
		{"J K", "reorder the selected priority entry"},
		{"enter x", "add the highlighted device, drop an entry"},
		{"w", "write changes to the config file"},
		{"", ""},
		{"Status", ""},
		{"j k", "scroll the event log"},
		{"i u", "install or remove the systemd user service"},
	}

	body := make([]string, 0, len(rows))
	for _, r := range rows {
		switch {
		case r.key == "":
			body = append(body, "")
		case r.desc == "":
			body = append(body, "  "+styleSubtitle.Render(r.key))
		default:
			body = append(body, "  "+styleKey.Render(fit(r.key, 9))+
				styleMuted.Render(truncate(r.desc, width-11)))
		}
	}

	return frame(height,
		[]string{"  " + styleTitle.Render("Keys"), ""},
		body,
		[]string{"", helpLine(width, "? or esc to close")},
	)
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
			// Carry the error so the config screen keeps showing the last
			// good list instead of blanking out on a transient hiccup.
			result.priorities = prioritiesMsg{err: devErr}
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
