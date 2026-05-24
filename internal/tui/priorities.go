package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea/v2"
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

type PrioritiesModel struct {
	priorities []config.PriorityEntry
	devices    []audio.Device
	switching  config.SwitchingConfig

	section       configSection
	prioFocus     priorityFocus
	prioCursor    int
	availCursor   int
	switchCursor  int
	dirty         bool
	switchDirty   bool
	err           error
	saveMsg       string
}

func NewPrioritiesModel() PrioritiesModel {
	return PrioritiesModel{}
}

func (m PrioritiesModel) Update(msg tea.Msg) (PrioritiesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.section {
		case sectionPriorities:
			return m.handlePriorityInput(msg)
		case sectionSwitching:
			return m.handleSwitchingInput(msg)
		}
	case prioritiesMsg:
		m.priorities = msg.priorities
		m.devices = msg.devices
		m.switching = msg.switching
		m.err = msg.err
		if !m.dirty {
			m.clampCursors()
		}
	case savePrioritiesResultMsg:
		m.err = msg.err
		if msg.err == nil {
			m.dirty = false
			m.saveMsg = "Saved!"
		}
	case saveSwitchingResultMsg:
		m.err = msg.err
		if msg.err == nil {
			m.switchDirty = false
			m.saveMsg = "Saved!"
		}
	}
	return m, nil
}

func (m PrioritiesModel) handlePriorityInput(msg tea.KeyMsg) (PrioritiesModel, tea.Cmd) {
	available := m.availableDevices()

	switch msg.String() {
	case "tab":
		m.section = sectionSwitching
		m.switchCursor = 0
		m.saveMsg = ""
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
			m.saveMsg = ""
		}
	case "J", "shift+down":
		if m.prioFocus == focusPriorityList && m.prioCursor < len(m.priorities)-1 {
			m.priorities[m.prioCursor], m.priorities[m.prioCursor+1] = m.priorities[m.prioCursor+1], m.priorities[m.prioCursor]
			m.prioCursor++
			m.dirty = true
			m.saveMsg = ""
		}
	case "enter":
		if m.prioFocus == focusAvailableList && m.availCursor < len(available) {
			dev := available[m.availCursor]
			entry := config.PriorityEntry{
				Match: dev.Name,
				Type:  strings.ToLower(dev.Type.String()),
			}
			m.priorities = append(m.priorities, entry)
			m.dirty = true
			m.saveMsg = ""
			m.clampCursors()
		}
	case "x":
		if m.prioFocus == focusPriorityList && len(m.priorities) > 0 && m.prioCursor < len(m.priorities) {
			m.priorities = append(m.priorities[:m.prioCursor], m.priorities[m.prioCursor+1:]...)
			m.dirty = true
			m.saveMsg = ""
			m.clampCursors()
		}
	case "w":
		if m.dirty {
			return m, savePrioritiesCmd(m.priorities)
		}
	}
	return m, nil
}

func (m PrioritiesModel) handleSwitchingInput(msg tea.KeyMsg) (PrioritiesModel, tea.Cmd) {
	opts := m.switchingOptions()
	total := len(opts)

	switch msg.String() {
	case "tab":
		m.section = sectionPriorities
		m.saveMsg = ""
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
		m.saveMsg = ""
	case "w":
		if m.switchDirty {
			return m, saveSwitchingCmd(m.switching)
		}
	}
	return m, nil
}

func (m PrioritiesModel) View() string {
	var b strings.Builder

	tabs := []string{"Devices", "Switching"}
	for i, t := range tabs {
		label := t
		if configSection(i) == m.section {
			b.WriteString(styleActiveTab.Render("[ " + label + " ]"))
		} else {
			b.WriteString(styleTab.Render("  " + label + "  "))
		}
		b.WriteString("  ")
	}
	b.WriteString(styleMuted.Render("(Tab to switch)"))
	b.WriteString("\n\n")

	switch m.section {
	case sectionPriorities:
		b.WriteString(m.viewPriorities())
	case sectionSwitching:
		b.WriteString(m.viewSwitching())
	}

	if m.saveMsg != "" {
		b.WriteString("\n")
		b.WriteString(styleActive.Render("  " + m.saveMsg))
	}

	return b.String()
}

func (m PrioritiesModel) viewPriorities() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("Device Priority"))
	dirty := ""
	if m.dirty {
		dirty = " (unsaved)"
	}
	if dirty != "" {
		b.WriteString(styleMuted.Render(dirty))
	}
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("Highest priority first — fallback uses this order"))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(styleMuted.Render(fmt.Sprintf("Error: %v\n\n", m.err)))
	}

	if len(m.priorities) == 0 {
		b.WriteString(styleMuted.Render("  No priorities configured yet"))
		b.WriteString("\n")
	} else {
		for i, p := range m.priorities {
			cursor := "  "
			if m.prioFocus == focusPriorityList && i == m.prioCursor {
				cursor = "> "
			}

			typeStr := ""
			if p.Type != "" {
				typeStr = fmt.Sprintf("  %-10s", p.Type)
			}

			line := fmt.Sprintf("%s%d. %s%s", cursor, i+1, p.Match, typeStr)
			if m.prioFocus == focusPriorityList && i == m.prioCursor {
				b.WriteString(styleSelected.Render(line))
			} else {
				b.WriteString(styleNormal.Render(line))
			}
			b.WriteString("\n")
		}
	}

	available := m.availableDevices()
	b.WriteString("\n")
	b.WriteString(styleSubtitle.Render("Available Devices"))
	b.WriteString("\n")

	if len(available) == 0 {
		b.WriteString(styleMuted.Render("  All devices are prioritized"))
		b.WriteString("\n")
	} else {
		for i, dev := range available {
			cursor := "  "
			if m.prioFocus == focusAvailableList && i == m.availCursor {
				cursor = "> "
			}

			typeName := fmt.Sprintf("%-10s", dev.Type.String())
			line := fmt.Sprintf("%s  %s  %s", cursor, dev.Name, typeName)

			if m.prioFocus == focusAvailableList && i == m.availCursor {
				b.WriteString(styleSelected.Render(line))
			} else {
				b.WriteString(styleMuted.Render(line))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(styleHelp.Render("[Enter] Add  [x] Remove  [J/K] Reorder  [w] Save  [Tab] Switching"))

	return b.String()
}

func (m PrioritiesModel) viewSwitching() string {
	var b strings.Builder

	dirty := ""
	if m.switchDirty {
		dirty = " (unsaved)"
	}

	b.WriteString(styleTitle.Render("On Bluetooth Connect"))
	if dirty != "" {
		b.WriteString(styleMuted.Render(dirty))
	}
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("What happens when a Bluetooth device connects"))
	b.WriteString("\n\n")

	connectOpts := []struct {
		value string
		label string
	}{
		{"always", "Always switch to it"},
		{"priority", "Only if higher priority than current"},
		{"never", "Never auto-switch"},
	}

	for i, opt := range connectOpts {
		cursor := "  "
		if m.switchCursor == i {
			cursor = "> "
		}
		radio := "( )"
		if m.switching.OnConnect == opt.value {
			radio = "(*)"
		}
		line := fmt.Sprintf("%s%s %s", cursor, radio, opt.label)
		if m.switchCursor == i {
			b.WriteString(styleSelected.Render(line))
		} else {
			b.WriteString(styleNormal.Render(line))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styleTitle.Render("On Disconnect (Fallback)"))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("What happens when the current device disconnects"))
	b.WriteString("\n\n")

	disconnectOpts := []struct {
		value string
		label string
	}{
		{"priority", "Switch to highest priority available"},
		{"previous", "Switch to previous device"},
	}

	offset := len(connectOpts)
	for i, opt := range disconnectOpts {
		idx := offset + i
		cursor := "  "
		if m.switchCursor == idx {
			cursor = "> "
		}
		radio := "( )"
		if m.switching.OnDisconnect == opt.value {
			radio = "(*)"
		}
		line := fmt.Sprintf("%s%s %s", cursor, radio, opt.label)
		if m.switchCursor == idx {
			b.WriteString(styleSelected.Render(line))
		} else {
			b.WriteString(styleNormal.Render(line))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styleHelp.Render("[Enter] Toggle  [w] Save  [Tab] Devices"))

	return b.String()
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

func (m PrioritiesModel) switchingOptions() []string {
	return []string{"always", "priority", "never", "priority", "previous"}
}

func (m *PrioritiesModel) applySwitch(cursor int) {
	connectOpts := []string{"always", "priority", "never"}
	disconnectOpts := []string{"priority", "previous"}

	if cursor < len(connectOpts) {
		m.switching.OnConnect = connectOpts[cursor]
	} else {
		idx := cursor - len(connectOpts)
		if idx < len(disconnectOpts) {
			m.switching.OnDisconnect = disconnectOpts[idx]
		}
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
