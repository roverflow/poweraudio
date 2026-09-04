package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
)

type configSection int

const (
	sectionPriorities configSection = iota
	sectionSwitching
)

type priorityFocus int

const (
	focusPriorityList priorityFocus = iota
	focusAvailableList
)

// prioTypeW is the width of the type column on both lists, wide enough for
// "bluetooth".
const prioTypeW = 10

type PrioritiesModel struct {
	priorities []config.PriorityEntry
	devices    []audio.Device
	switching  config.SwitchingConfig

	section      configSection
	prioFocus    priorityFocus
	prioCursor   int
	availCursor  int
	switchCursor int
	offset       int
	dirty        bool
	switchDirty  bool
	err          error

	width  int
	height int
}

func NewPrioritiesModel() PrioritiesModel {
	return PrioritiesModel{width: defaultWidth, height: defaultHeight - chromeLines}
}

func (m PrioritiesModel) hasUnsaved() bool {
	return m.dirty || m.switchDirty
}

func (m PrioritiesModel) Update(msg tea.Msg) (PrioritiesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		var cmd tea.Cmd
		switch m.section {
		case sectionPriorities:
			m, cmd = m.handlePriorityInput(msg)
		case sectionSwitching:
			m, cmd = m.handleSwitchingInput(msg)
		}
		m.syncScroll()
		return m, cmd

	case prioritiesMsg:
		m.err = msg.err
		if msg.err != nil {
			// A failed refresh says nothing about the config. Keep showing
			// what we last read rather than blanking the screen.
			return m, nil
		}
		m.devices = msg.devices
		if !m.dirty {
			m.priorities = msg.priorities
			m.clampCursors()
		}
		if !m.switchDirty {
			m.switching = msg.switching
		}
		m.syncScroll()

	case savePrioritiesResultMsg:
		m.err = msg.err
		if msg.err == nil {
			m.dirty = false
		}

	case saveSwitchingResultMsg:
		m.err = msg.err
		if msg.err == nil {
			m.switchDirty = false
		}
	}
	return m, nil
}

func (m PrioritiesModel) handlePriorityInput(msg tea.KeyPressMsg) (PrioritiesModel, tea.Cmd) {
	available := m.availableDevices()

	switch msg.String() {
	case "tab":
		m.section = sectionSwitching
		m.switchCursor = 0
	case "up", "k":
		if m.prioFocus == focusPriorityList {
			if m.prioCursor > 0 {
				m.prioCursor--
			}
		} else {
			if m.availCursor > 0 {
				m.availCursor--
			} else {
				m.prioFocus = focusPriorityList
				if len(m.priorities) > 0 {
					m.prioCursor = len(m.priorities) - 1
				}
			}
		}
	case "down", "j":
		if m.prioFocus == focusPriorityList {
			if m.prioCursor < len(m.priorities)-1 {
				m.prioCursor++
			} else if len(available) > 0 {
				m.prioFocus = focusAvailableList
				m.availCursor = 0
			}
		} else {
			if m.availCursor < len(available)-1 {
				m.availCursor++
			}
		}
	case "K", "shift+up":
		if m.prioFocus == focusPriorityList && m.prioCursor > 0 {
			m.priorities[m.prioCursor], m.priorities[m.prioCursor-1] = m.priorities[m.prioCursor-1], m.priorities[m.prioCursor]
			m.prioCursor--
			m.dirty = true
		}
	case "J", "shift+down":
		if m.prioFocus == focusPriorityList && m.prioCursor < len(m.priorities)-1 {
			m.priorities[m.prioCursor], m.priorities[m.prioCursor+1] = m.priorities[m.prioCursor+1], m.priorities[m.prioCursor]
			m.prioCursor++
			m.dirty = true
		}
	case "enter":
		if m.prioFocus == focusAvailableList && m.availCursor < len(available) {
			dev := available[m.availCursor]
			m.priorities = append(m.priorities, config.PriorityEntry{
				Match: dev.Name,
				Type:  strings.ToLower(dev.Type.String()),
			})
			m.dirty = true
			m.clampCursors()
		}
	case "x":
		if m.prioFocus == focusPriorityList && len(m.priorities) > 0 && m.prioCursor < len(m.priorities) {
			m.priorities = append(m.priorities[:m.prioCursor], m.priorities[m.prioCursor+1:]...)
			m.dirty = true
			m.clampCursors()
		}
	case "w":
		if m.dirty {
			return m, savePrioritiesCmd(m.priorities)
		}
	}
	return m, nil
}

func (m PrioritiesModel) handleSwitchingInput(msg tea.KeyPressMsg) (PrioritiesModel, tea.Cmd) {
	total := len(connectOptions) + len(disconnectOptions)

	switch msg.String() {
	case "tab":
		m.section = sectionPriorities
	case "up", "k":
		if m.switchCursor > 0 {
			m.switchCursor--
		}
	case "down", "j":
		if m.switchCursor < total-1 {
			m.switchCursor++
		}
	case "enter", " ":
		m.applySwitch(m.switchCursor)
		m.switchDirty = true
	case "w":
		if m.switchDirty {
			return m, saveSwitchingCmd(m.switching)
		}
	}
	return m, nil
}

func (m PrioritiesModel) View() string {
	w := m.width
	if w < minWidth {
		w = minWidth
	}
	h := m.height
	if h < minContentH {
		h = minContentH
	}

	tabs := []string{"Priorities", "Switching"}
	var bar string
	for i, t := range tabs {
		if configSection(i) == m.section {
			bar += styleActiveTab.Render(t)
		} else {
			bar += styleTab.Render(t)
		}
	}
	bar += styleMuted.Render("  tab to swap")

	if m.section == sectionSwitching {
		return m.viewSwitching(w, h, bar)
	}
	return m.viewPriorities(w, h, bar)
}

func (m PrioritiesModel) viewPriorities(w, h int, bar string) string {
	visible := m.rowCount()
	nameW := m.nameWidth()

	title := "  " + styleTitle.Render("Device Priority")
	if m.dirty {
		title += styleWarn.Render("  unsaved")
	}
	if hint := scrollHint(m.offset, visible, m.bodyLen()); hint != "" {
		title += styleMuted.Render("   " + hint)
	}

	header := []string{
		bar,
		"",
		title,
		"  " + styleMuted.Render(truncate("Highest first. A green dot marks an entry that is plugged in right now.", w-2)),
		"",
	}

	var rows []string
	if len(m.priorities) == 0 {
		rows = append(rows, "  "+styleMuted.Render("nothing yet, pick a device below and press enter"))
	} else {
		for i, p := range m.priorities {
			rows = append(rows, m.priorityRow(i, p, nameW))
		}
	}

	rows = append(rows, "", "  "+styleSubtitle.Render("Available Devices"))

	available := m.availableDevices()
	if len(available) == 0 {
		rows = append(rows, "  "+styleMuted.Render("every detected device is already on the list"))
	} else {
		for i, dev := range available {
			rows = append(rows, m.availableRow(i, dev, nameW))
		}
	}

	footer := []string{
		"",
		helpLine(w, "enter add", "x remove", "J/K reorder", "w save", "tab switching", "? help"),
	}

	return frame(h, header, window(rows, m.offset, visible), footer)
}

func (m PrioritiesModel) priorityRow(i int, p config.PriorityEntry, nameW int) string {
	selected := m.prioFocus == focusPriorityList && i == m.prioCursor

	prefix := "  "
	if selected {
		prefix = styleAccent.Render("▎") + " "
	}

	live := "  "
	if m.priorityPresent(p) {
		live = styleActive.Render("●") + " "
	}

	text := fmt.Sprintf("%2d. ", i+1) + fit(p.Match, nameW) + "  " + fit(p.Type, prioTypeW)
	if selected {
		text = styleSelected.Render(text)
	} else {
		text = styleNormal.Render(text)
	}
	return prefix + live + text
}

func (m PrioritiesModel) availableRow(i int, dev audio.Device, nameW int) string {
	selected := m.prioFocus == focusAvailableList && i == m.availCursor

	prefix := "  "
	if selected {
		prefix = styleAccent.Render("▎") + " "
	}

	// Four spaces where the priority rows carry their rank, so both lists
	// line up in the same columns.
	text := "    " + fit(dev.Name, nameW) + "  " + fit(dev.Type.String(), prioTypeW)
	if selected {
		text = styleSelected.Render(text)
	} else {
		text = styleMuted.Render(text)
	}
	return prefix + "  " + text
}

var connectOptions = []struct{ value, label string }{
	{"always", "Always switch to it"},
	{"priority", "Only if it outranks the current output"},
	{"never", "Never switch on its own"},
}

var disconnectOptions = []struct{ value, label string }{
	{"priority", "Fall back to the highest ranked device available"},
	{"previous", "Fall back to whatever was playing before"},
}

func (m PrioritiesModel) viewSwitching(w, h int, bar string) string {
	header := []string{bar, ""}

	radio := func(idx int, on bool, label string) string {
		prefix := "  "
		if m.switchCursor == idx {
			prefix = styleAccent.Render("▎") + " "
		}
		mark := styleMuted.Render("( )")
		if on {
			mark = styleActive.Render("(•)")
		}
		text := truncate(label, w-8)
		if m.switchCursor == idx {
			text = styleSelected.Render(text)
		} else {
			text = styleNormal.Render(text)
		}
		return prefix + mark + " " + text
	}

	title := "  " + styleTitle.Render("On Bluetooth Connect")
	if m.switchDirty {
		title += styleWarn.Render("  unsaved")
	}

	body := []string{
		title,
		"  " + styleMuted.Render("when a Bluetooth device pairs up"),
		"",
	}
	for i, opt := range connectOptions {
		body = append(body, radio(i, m.switching.OnConnect == opt.value, opt.label))
	}

	body = append(body,
		"",
		"  "+styleTitle.Render("On Disconnect"),
		"  "+styleMuted.Render("when the device you are listening on goes away"),
		"",
	)
	for i, opt := range disconnectOptions {
		body = append(body, radio(len(connectOptions)+i, m.switching.OnDisconnect == opt.value, opt.label))
	}

	footer := []string{
		"",
		helpLine(w, "enter pick", "w save", "tab priorities", "? help"),
	}

	return frame(h, header, body, footer)
}

// rowCount is the list space left after the section tabs, a blank, the title,
// the subtitle, a blank, a blank and the help line.
func (m PrioritiesModel) rowCount() int {
	if n := m.height - 7; n > 0 {
		return n
	}
	return 1
}

// nameWidth leaves room for the cursor gutter, the live dot, the rank, the gap
// and the type column.
func (m PrioritiesModel) nameWidth() int {
	w := m.width
	if w < minWidth {
		w = minWidth
	}
	if n := w - 1 - (2 + 2 + 4 + 2 + prioTypeW); n > 12 {
		return n
	}
	return 12
}

// bodyLen counts the rows viewPriorities builds, so scrolling and the position
// hint agree with what is on screen.
func (m PrioritiesModel) bodyLen() int {
	n := len(m.priorities)
	if n == 0 {
		n = 1
	}
	a := len(m.availableDevices())
	if a == 0 {
		a = 1
	}
	return n + 2 + a
}

// selectedRow maps the two cursors onto the single flat list that bodyLen
// counts.
func (m PrioritiesModel) selectedRow() int {
	if m.prioFocus == focusPriorityList {
		if len(m.priorities) == 0 {
			return 0
		}
		return m.prioCursor
	}
	n := len(m.priorities)
	if n == 0 {
		n = 1
	}
	return n + 2 + m.availCursor
}

func (m *PrioritiesModel) syncScroll() {
	if m.section != sectionPriorities {
		m.offset = 0
		return
	}
	m.offset = clampOffset(m.offset, m.selectedRow(), m.bodyLen(), m.rowCount())
}

func (m PrioritiesModel) availableDevices() []audio.Device {
	var available []audio.Device
	for _, dev := range m.devices {
		found := false
		for _, p := range m.priorities {
			if strings.EqualFold(p.Match, dev.Name) {
				found = true
				break
			}
		}
		if !found {
			available = append(available, dev)
		}
	}
	return available
}

// priorityPresent reports whether a configured entry matches a sink that exists
// right now. It uses the same comparison as availableDevices so the two lists
// never disagree about a device.
func (m PrioritiesModel) priorityPresent(p config.PriorityEntry) bool {
	for _, dev := range m.devices {
		if strings.EqualFold(p.Match, dev.Name) {
			return true
		}
	}
	return false
}

func (m *PrioritiesModel) applySwitch(cursor int) {
	if cursor < len(connectOptions) {
		m.switching.OnConnect = connectOptions[cursor].value
		return
	}
	if idx := cursor - len(connectOptions); idx < len(disconnectOptions) {
		m.switching.OnDisconnect = disconnectOptions[idx].value
	}
}

func (m *PrioritiesModel) clampCursors() {
	if m.prioCursor >= len(m.priorities) {
		m.prioCursor = max(0, len(m.priorities)-1)
	}
	available := m.availableDevices()
	if m.availCursor >= len(available) {
		m.availCursor = max(0, len(available)-1)
	}
	if m.prioFocus == focusAvailableList && len(available) == 0 {
		m.prioFocus = focusPriorityList
	}
}

type prioritiesMsg struct {
	priorities []config.PriorityEntry
	devices    []audio.Device
	switching  config.SwitchingConfig
	err        error
}

type savePrioritiesMsg struct {
	priorities []config.PriorityEntry
}

type savePrioritiesResultMsg struct {
	err error
}

type saveSwitchingMsg struct {
	switching config.SwitchingConfig
}

type saveSwitchingResultMsg struct {
	err error
}

func savePrioritiesCmd(priorities []config.PriorityEntry) tea.Cmd {
	return func() tea.Msg {
		return savePrioritiesMsg{priorities: priorities}
	}
}

func saveSwitchingCmd(switching config.SwitchingConfig) tea.Cmd {
	return func() tea.Msg {
		return saveSwitchingMsg{switching: switching}
	}
}
