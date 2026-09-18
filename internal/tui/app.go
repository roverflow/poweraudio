package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/ipc"
)

type screen int

const (
	screenDevices screen = iota
	screenConfig
	screenStatus
	screenHelp
)

type noticeKind int

const (
	noticeInfo noticeKind = iota
	noticeWarn
	noticeErr
)

const (
	// noticeTTL is how long an action result stays in the status bar.
	noticeTTL = 5 * time.Second

	// tabRows is the tab bar and the blank line under it, which is where the
	// content area starts for the purposes of a mouse click.
	tabRows = 2

	// wheelRows is how far one notch scrolls a log. Lists with a cursor move
	// a single row per notch instead, so the wheel does not throw the
	// selection clean across the screen.
	wheelRows = 3
)

type notice struct {
	text string
	kind noticeKind
	at   time.Time
}

// Model is the whole UI. It owns no audio state: every screen is a view over
// the last snapshot the daemon pushed, and every action is a request back.
type Model struct {
	screen     screen
	prevScreen screen

	devices devicesModel
	config  configModel
	status  statusModel
	help    helpModel

	client *ipc.Client
	width  int
	height int

	// gen is the subscription generation. Messages from an older one are
	// dropped, which is what keeps a dead connection from being revived by a
	// retry timer that a manual reconnect already overtook.
	gen       int
	sub       <-chan ipc.Snapshot
	connected bool

	uptimeTicking bool
	note          notice
	confirmQuit   bool
}

func NewModel(client *ipc.Client) Model {
	m := Model{
		screen:     screenDevices,
		prevScreen: screenDevices,
		devices:    newDevicesModel(),
		config:     newConfigModel(),
		status:     newStatusModel(),
		help:       newHelpModel(),
		client:     client,
		width:      defaultWidth,
		height:     defaultHeight,
	}
	m.applySize()
	return m
}

func (m Model) Init() tea.Cmd {
	return subscribeCmd(m.client, m.gen)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseClickMsg:
		return m.handleClick(msg.Mouse())

	case tea.MouseWheelMsg:
		return m.handleWheel(msg.Mouse())

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.applySize()
		return m, nil

	case subReadyMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.sub = msg.ch
		return m, waitSnapshotCmd(msg.ch, msg.gen)

	case snapshotMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.applySnapshot(msg.snap)
		return m, waitSnapshotCmd(m.sub, msg.gen)

	case subClosedMsg:
		return m.dropped(msg.gen)

	case subFailedMsg:
		return m.dropped(msg.gen)

	case subRetryMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		return m, subscribeCmd(m.client, m.gen)

	case refreshMsg:
		if msg.err != nil {
			m.connected = false
			return m, m.notify(noticeErr, "Refresh failed: "+msg.err.Error())
		}
		m.applySnapshot(*msg.snap)
		return m, nil

	case noticeMsg:
		return m, m.notify(msg.kind, msg.text)

	case noticeExpiredMsg:
		if msg.at.Equal(m.note.at) {
			m.note = notice{}
		}
		return m, nil

	case uptimeTickMsg:
		// The status screen is the only one that shows a clock, so the tick
		// stops as soon as you leave it.
		m.uptimeTicking = m.screen == screenStatus
		if !m.uptimeTicking {
			return m, nil
		}
		return m, uptimeTickCmd()

	case switchDeviceMsg:
		return m, setDefaultCmd(m.client, msg.deviceID)

	case setDefaultMsg:
		if msg.err != nil {
			return m, m.notify(noticeErr, "Set default failed: "+msg.err.Error())
		}
		return m, m.notify(noticeInfo, "Default output changed")

	case volumeMsg:
		return m, setVolumeCmd(m.client, msg.deviceID, msg.percent)

	case volumeTickMsg:
		var cmd tea.Cmd
		m.devices, cmd = m.devices.volumeTick(msg.seq)
		return m, cmd

	case volumeResultMsg:
		var cmd tea.Cmd
		m.devices, cmd = m.devices.volumeDone()
		if msg.err != nil {
			return m, tea.Batch(cmd, m.notify(noticeErr, "Volume change failed: "+msg.err.Error()))
		}
		return m, cmd

	case muteMsg:
		return m, toggleMuteCmd(m.client, msg.deviceID)

	case muteResultMsg:
		if msg.err != nil {
			return m, m.notify(noticeErr, "Mute toggle failed: "+msg.err.Error())
		}
		return m, nil

	case savePrioritiesMsg:
		return m, updatePrioritiesCmd(m.client, msg.priorities)

	case savePrioritiesResultMsg:
		if msg.err != nil {
			return m, m.notify(noticeErr, "Save failed: "+msg.err.Error())
		}
		m.config.saved()
		return m, m.notify(noticeInfo, "Priorities saved")

	case saveSwitchingMsg:
		return m, updateSwitchingCmd(m.client, msg.switching)

	case saveSwitchingResultMsg:
		if msg.err != nil {
			return m, m.notify(noticeErr, "Save failed: "+msg.err.Error())
		}
		m.config.switchingSaved()
		return m, m.notify(noticeInfo, "Switching rules saved")

	case serviceActionMsg:
		m.status.checkService(true)
		if msg.err != nil {
			return m, m.notify(noticeErr, "Failed: "+msg.err.Error())
		}
		return m, m.notify(noticeInfo, msg.status)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key != "q" {
		m.confirmQuit = false
	}

	switch key {
	case "ctrl+c":
		return m, tea.Quit

	case "q":
		// Reordering a priority list and forgetting to press w used to throw
		// the work away without a word. Ask once.
		if m.config.hasUnsaved() && !m.confirmQuit {
			m.confirmQuit = true
			return m, m.notify(noticeWarn, "Unsaved config. q again to discard, w to save.")
		}
		return m, tea.Quit

	case "?":
		if m.screen == screenHelp {
			return m.show(m.prevScreen)
		}
		m.prevScreen = m.screen
		return m.show(screenHelp)

	case "esc":
		if m.screen == screenHelp {
			return m.show(m.prevScreen)
		}

	case "d", "1":
		return m.show(screenDevices)
	case "c", "2":
		return m.show(screenConfig)
	case "s", "3":
		return m.show(screenStatus)

	case "r":
		cmds := []tea.Cmd{snapshotOnceCmd(m.client)}
		if !m.connected {
			m.gen++
			cmds = append(cmds, subscribeCmd(m.client, m.gen))
		}
		return m, tea.Batch(cmds...)
	}

	var cmd tea.Cmd
	switch m.screen {
	case screenDevices:
		m.devices, cmd = m.devices.Update(msg)
	case screenConfig:
		m.config, cmd = m.config.Update(msg)
	case screenStatus:
		m.status, cmd = m.status.Update(msg)
	case screenHelp:
		m.help, cmd = m.help.Update(msg)
	}
	return m, cmd
}

// handleClick sends a click on the tab bar to the tabs and anything below it
// to the active screen, in that screen's own row coordinates.
func (m Model) handleClick(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if mouse.Button != tea.MouseLeft {
		return m, nil
	}

	w, h := m.contentSize()
	if mouse.Y == 0 {
		if s, ok := tabAt(w, mouse.X); ok {
			return m.show(s)
		}
		return m, nil
	}

	row := mouse.Y - tabRows
	if row < 0 || row >= h {
		return m, nil
	}

	var cmd tea.Cmd
	switch m.screen {
	case screenDevices:
		m.devices, cmd = m.devices.click(row)
	case screenConfig:
		m.config, cmd = m.config.click(row)
	}
	return m, cmd
}

func (m Model) handleWheel(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	delta := 0
	switch mouse.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return m, nil
	}

	var cmd tea.Cmd
	switch m.screen {
	case screenDevices:
		m.devices, cmd = m.devices.scroll(delta)
	case screenConfig:
		m.config, cmd = m.config.scroll(delta)
	case screenStatus:
		m.status, cmd = m.status.scroll(delta * wheelRows)
	case screenHelp:
		m.help, cmd = m.help.scroll(delta * wheelRows)
	}
	return m, cmd
}

// show switches screens and starts the uptime clock when the status screen
// comes up.
func (m Model) show(s screen) (tea.Model, tea.Cmd) {
	m.screen = s
	if s != screenStatus || m.uptimeTicking {
		return m, nil
	}
	m.uptimeTicking = true
	return m, uptimeTickCmd()
}

// dropped handles the subscription going away, from either end.
func (m Model) dropped(gen int) (tea.Model, tea.Cmd) {
	if gen != m.gen {
		return m, nil
	}
	m.connected = false
	m.sub = nil
	m.gen++
	return m, retrySubscribeCmd(m.gen)
}

func (m *Model) applySnapshot(snap ipc.Snapshot) {
	m.connected = true
	m.devices.setSnapshot(snap.Devices, snap.Config.Priority)
	m.config.setSnapshot(snap.Devices, snap.Config)
	m.status.setSnapshot(snap)
}

func (m Model) View() tea.View {
	w, _ := m.contentSize()

	var content string
	switch m.screen {
	case screenDevices:
		content = m.devices.View()
	case screenConfig:
		content = m.config.View()
	case screenStatus:
		content = m.status.View()
	case screenHelp:
		content = m.help.View()
	}

	// Exactly h lines of content, so the whole frame comes to h+chromeLines
	// and the status bar always lands on the bottom row.
	body := strings.Join([]string{
		m.renderTabs(w),
		"",
		content,
		"",
		m.renderStatusBar(w),
	}, "\n")

	v := tea.NewView(body)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// contentSize is the space left for the active screen once the tab bar, the
// blank lines and the status bar have taken their rows.
func (m Model) contentSize() (int, int) {
	return screenSize(m.width, m.height-chromeLines)
}

func (m *Model) applySize() {
	w, h := m.contentSize()

	m.devices.width, m.devices.height = w, h
	m.config.width, m.config.height = w, h
	m.status.width, m.status.height = w, h
	m.help.width, m.help.height = w, h

	m.devices.syncScroll()
	m.config.syncScroll()
}

func (m *Model) notify(kind noticeKind, text string) tea.Cmd {
	m.note = notice{text: text, kind: kind, at: time.Now()}
	return noticeExpiryCmd(m.note.at)
}

var tabs = []struct {
	key    string
	name   string
	screen screen
}{
	{"d", "Devices", screenDevices},
	{"c", "Config", screenConfig},
	{"s", "Status", screenStatus},
}

// tabLabels drops the words on a terminal too narrow for the full bar, since
// a tab bar that overflows wraps and pushes the frame off by a line.
func tabLabels(width int) []string {
	full := make([]string, len(tabs))
	total := 0
	for i, t := range tabs {
		full[i] = t.key + " " + t.name
		total += runeLen(full[i]) + 4
	}
	if total <= width {
		return full
	}
	short := make([]string, len(tabs))
	for i, t := range tabs {
		short[i] = t.key
	}
	return short
}

// tabAt is the tab the pointer landed on, taking the two columns of padding
// each side into account.
func tabAt(width, x int) (screen, bool) {
	at := 0
	for i, label := range tabLabels(width) {
		end := at + runeLen(label) + 4
		if x >= at && x < end {
			return tabs[i].screen, true
		}
		at = end
	}
	return screenDevices, false
}

func (m Model) renderTabs(width int) string {
	labels := tabLabels(width)
	parts := make([]string, 0, len(labels))
	for i, label := range labels {
		if tabs[i].screen == m.screen {
			parts = append(parts, styleActiveTab.Render(label))
		} else {
			parts = append(parts, styleTab.Render(label))
		}
	}
	return strings.Join(parts, "")
}

// renderStatusBar keeps the daemon state on the left and the most recent
// action result on the right. The key hints live on each screen's help line
// instead, so there is only one place to look for them.
func (m Model) renderStatusBar(width int) string {
	state, dot := "daemon offline", styleError.Render("●")
	if m.connected {
		state, dot = "daemon ready", styleActive.Render("●")
	}

	leftPlain := "  ● " + state
	left := "  " + dot + styleStatusBar.Render(" "+state)

	rightPlain := ""
	right := ""
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
