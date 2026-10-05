package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/priority"
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

const (
	// configHeaderRows is the section tabs, a blank, the title, the subtitle
	// and a blank.
	configHeaderRows = 5

	// switchHeaderRows is the section tabs and a blank.
	switchHeaderRows = 2
)

type configModel struct {
	priorities []config.PriorityEntry
	devices    []audio.Device
	switching  config.SwitchingConfig
	ready      bool

	section      configSection
	prioFocus    priorityFocus
	prioCursor   int
	availCursor  int
	switchCursor int
	offset       int
	dirty        bool
	switchDirty  bool

	width  int
	height int
}

func newConfigModel() configModel {
	return configModel{width: defaultWidth, height: defaultHeight - chromeLines}
}

func (m configModel) hasUnsaved() bool {
	return m.dirty || m.switchDirty
}

func (m configModel) Update(msg tea.Msg) (configModel, tea.Cmd) {
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
	}
	return m, nil
}

func (m *configModel) setSnapshot(devices []audio.Device, cfg config.Config) {
	m.devices = devices
	m.ready = true
	if !m.dirty {
		m.priorities = cfg.Priority
		m.clampCursors()
	}
	if !m.switchDirty {
		m.switching = cfg.Switching
	}
	m.syncScroll()
}

func (m *configModel) saved() {
	m.dirty = false
}

func (m *configModel) switchingSaved() {
	m.switchDirty = false
}

func (m configModel) handlePriorityInput(msg tea.KeyPressMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "tab":
		m.section = sectionSwitching
		m.switchCursor = 0
	case "up", "k":
		m = m.stepSelection(-1)
	case "down", "j":
		m = m.stepSelection(1)
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
		return m.activate()
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

func (m configModel) activate() (configModel, tea.Cmd) {
	if m.prioFocus == focusAvailableList {
		available := m.availableDevices()
		if m.availCursor >= len(available) {
			return m, nil
		}
		dev := available[m.availCursor]
		m.priorities = append(m.priorities, config.PriorityEntry{
			Match: dev.Name,
			Type:  strings.ToLower(dev.Type.String()),
		})
		m.dirty = true
		m.clampCursors()
		return m, nil
	}

	if m.prioCursor >= len(m.priorities) {
		return m, nil
	}
	entry := m.priorities[m.prioCursor]
	for _, dev := range m.devices {
		if dev.Available && priority.Matches(dev, entry) {
			return m, requestDefaultCmd(dev.ID)
		}
	}
	return m, noticeCmd(noticeWarn, entry.Match+" is not connected")
}

func (m configModel) handleSwitchingInput(msg tea.KeyPressMsg) (configModel, tea.Cmd) {
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
	case "enter", " ", "space":
		m.applySwitch(m.switchCursor)
		m.switchDirty = true
	case "w":
		if m.switchDirty {
			return m, saveSwitchingCmd(m.switching)
		}
	}
	return m, nil
}

func (m configModel) click(row int) (configModel, tea.Cmd) {
	if m.section == sectionSwitching {
		if idx := m.switchRowIndex(row); idx >= 0 {
			m.switchCursor = idx
		}
		return m, nil
	}

	flat := row - configHeaderRows
	if flat < 0 || flat >= m.rowCount() {
		return m, nil
	}
	kind, idx := m.rowTarget(m.offset + flat)
	switch kind {
	case configRowPriority:
		m.prioFocus, m.prioCursor = focusPriorityList, idx
	case configRowAvailable:
		m.prioFocus, m.availCursor = focusAvailableList, idx
	}
	m.syncScroll()
	return m, nil
}

func (m configModel) scroll(delta int) (configModel, tea.Cmd) {
	if m.section == sectionSwitching {
		total := len(connectOptions) + len(disconnectOptions)
		m.switchCursor = clampScroll(m.switchCursor+delta, total, 1)
		return m, nil
	}

	step := 1
	if delta < 0 {
		step = -1
	}
	for i := 0; i < abs(delta); i++ {
		m = m.stepSelection(step)
	}
	m.syncScroll()
	return m, nil
}

// stepSelection walks the ranking and the available list as one list.
func (m configModel) stepSelection(dir int) configModel {
	available := m.availableDevices()

	if dir < 0 {
		if m.prioFocus == focusPriorityList {
			if m.prioCursor > 0 {
				m.prioCursor--
			}
			return m
		}
		if m.availCursor > 0 {
			m.availCursor--
			return m
		}
		m.prioFocus = focusPriorityList
		if len(m.priorities) > 0 {
			m.prioCursor = len(m.priorities) - 1
		}
		return m
	}

	if m.prioFocus == focusPriorityList {
		if m.prioCursor < len(m.priorities)-1 {
			m.prioCursor++
		} else if len(available) > 0 {
			m.prioFocus = focusAvailableList
			m.availCursor = 0
		}
		return m
	}
	if m.availCursor < len(available)-1 {
		m.availCursor++
	}
	return m
}

type configRowKind int

const (
	configRowNone configRowKind = iota
	configRowPriority
	configRowAvailable
)

// rowTarget maps a row of the flat list back onto one of the two lists.
func (m configModel) rowTarget(flat int) (configRowKind, int) {
	n := len(m.priorities)
	if flat < n {
		return configRowPriority, flat
	}
	if n == 0 {
		n = 1
	}
	idx := flat - n - 2
	if idx >= 0 && idx < len(m.availableDevices()) {
		return configRowAvailable, idx
	}
	return configRowNone, 0
}

func (m configModel) View() string {
	w, h := screenSize(m.width, m.height)

	bar := m.sectionBar(w)

	if m.section == sectionSwitching {
		return m.viewSwitching(w, h, bar)
	}
	return m.viewPriorities(w, h, bar)
}

func (m configModel) sectionBar(w int) string {
	const hint = "  tab to swap"

	names := []string{"Priorities", "Switching"}
	plain := 0
	var bar string
	for i, t := range names {
		plain += runeLen(t) + 4
		if configSection(i) == m.section {
			bar += styleActiveTab.Render(t)
		} else {
			bar += styleTab.Render(t)
		}
	}
	if plain+runeLen(hint) <= w-1 {
		bar += styleMuted.Render(hint)
	}
	return bar
}

func (m configModel) viewPriorities(w, h int, bar string) string {
	visible := m.rowCount()
	nameW, typeW := configColumns(w)

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
		"  " + styleMuted.Render(truncate("Highest first. A green dot marks an entry that is plugged in right now.", w-3)),
		"",
	}

	var rows []string
	switch {
	case !m.ready:
		rows = append(rows, "  "+styleMuted.Render("waiting for the daemon"))
	case len(m.priorities) == 0:
		rows = append(rows, "  "+styleMuted.Render("nothing yet, pick a device below and press enter"))
	default:
		for i, p := range m.priorities {
			rows = append(rows, m.priorityRow(i, p, w, nameW, typeW))
		}
	}

	rows = append(rows, "", "  "+styleSubtitle.Render("Available Devices"))

	available := m.availableDevices()
	if len(available) == 0 {
		rows = append(rows, "  "+styleMuted.Render("every detected device is already on the list"))
	} else {
		for i, dev := range available {
			rows = append(rows, m.availableRow(i, dev, w, nameW, typeW))
		}
	}

	footer := []string{
		"",
		helpLine(w, "enter add/play", "x drop", "J/K order", "w save", "tab rules", "? help", "q quit"),
	}

	return frame(h, header, window(rows, m.offset, visible), footer)
}

func (m configModel) priorityRow(i int, p config.PriorityEntry, width, nameW, typeW int) string {
	selected := m.prioFocus == focusPriorityList && i == m.prioCursor

	pieces := []rowPiece{}
	if priority.Present(p, m.devices) {
		pieces = append(pieces, keepPiece("●", styleActive, colorSuccess))
	} else {
		pieces = append(pieces, piece(" ", styleNormal))
	}
	pieces = append(pieces,
		piece(fmt.Sprintf(" %2d. ", i+1), styleMuted),
		piece(fit(p.Match, nameW), styleNormal),
	)
	if typeW > 0 {
		pieces = append(pieces, piece("  "+fit(typeLabel(p.Type), typeW), styleMuted))
	}
	return listRow(width, selected, pieces...)
}

func (m configModel) availableRow(i int, dev audio.Device, width, nameW, typeW int) string {
	selected := m.prioFocus == focusAvailableList && i == m.availCursor

	// Blank where ranked rows have their marker and rank, so columns line up.
	pieces := []rowPiece{
		piece("      ", styleNormal),
		piece(fit(dev.Name, nameW), styleMuted),
	}
	if typeW > 0 {
		pieces = append(pieces, piece("  "+fit(typeLabel(dev.Type.String()), typeW), styleMuted))
	}
	return listRow(width, selected, pieces...)
}

// typeLabel title-cases lowercase config types to match DeviceType.String.
func typeLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "any"
	}
	for _, t := range []audio.DeviceType{
		audio.DeviceTypeSpeaker,
		audio.DeviceTypeHeadphone,
		audio.DeviceTypeBluetooth,
		audio.DeviceTypeHDMI,
		audio.DeviceTypeUSB,
		audio.DeviceTypeUnknown,
	} {
		if strings.EqualFold(s, t.String()) {
			return t.String()
		}
	}
	r := []rune(strings.ToLower(s))
	return strings.ToUpper(string(r[0])) + string(r[1:])
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

func (m configModel) viewSwitching(w, h int, bar string) string {
	header := []string{bar, ""}

	radio := func(idx int, on bool, label string) string {
		mark := "( ) "
		style := styleMuted
		if on {
			mark = "(•) "
			style = styleActive
		}
		return listRow(w, idx == m.switchCursor,
			keepPiece(mark, style, colorSuccess),
			piece(truncate(label, w-8), styleNormal),
		)
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

	// Unranked devices tie, so priority mode with no ranking never switches.
	if warning := priorityModeWarning(m.switching.OnConnect, len(m.priorities)); warning != "" {
		body = append(body, "      "+styleWarn.Render(truncate(warning, w-8)))
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
		helpLine(w, "enter pick", "w save", "tab priorities", "? help", "q quit"),
	}

	return frame(h, header, body, footer)
}

func priorityModeWarning(onConnect string, entries int) string {
	if onConnect == "priority" && entries == 0 {
		return "needs at least one priority entry, otherwise nothing ever switches"
	}
	return ""
}

func (m configModel) switchRowIndex(row int) int {
	row -= switchHeaderRows + 3 // the title, the subtitle and a blank
	if row < 0 {
		return -1
	}
	if row < len(connectOptions) {
		return row
	}
	// Between the two groups sit the optional warning, a blank, the heading,
	// the subtitle and a blank.
	gap := 4
	if priorityModeWarning(m.switching.OnConnect, len(m.priorities)) != "" {
		gap++
	}
	idx := row - len(connectOptions) - gap
	if idx >= 0 && idx < len(disconnectOptions) {
		return len(connectOptions) + idx
	}
	return -1
}

func (m configModel) rowCount() int {
	_, h := screenSize(m.width, m.height)
	if n := h - configHeaderRows - 2; n > 0 {
		return n
	}
	return 1
}

func configColumns(width int) (nameW, typeW int) {
	// The row spends six columns on the presence marker and the rank.
	avail := width - 3 - 6

	typeW = deviceTypeW
	if avail < typeW+12 {
		typeW = 0
	}
	nameW = avail - typeW
	if typeW > 0 {
		nameW -= 2
	}
	if nameW < 1 {
		nameW = 1
	}
	return nameW, typeW
}

// bodyLen must match the rows viewPriorities builds.
func (m configModel) bodyLen() int {
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

func (m configModel) selectedRow() int {
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

func (m *configModel) syncScroll() {
	if m.section != sectionPriorities {
		m.offset = 0
		return
	}
	m.offset = clampOffset(m.offset, m.selectedRow(), m.bodyLen(), m.rowCount())
}

// availableDevices uses the daemon's matcher so the UI and the daemon agree
// on which devices are ranked.
func (m configModel) availableDevices() []audio.Device {
	return priority.Unranked(m.devices, m.priorities)
}

func (m *configModel) applySwitch(cursor int) {
	if cursor < len(connectOptions) {
		m.switching.OnConnect = connectOptions[cursor].value
		return
	}
	if idx := cursor - len(connectOptions); idx < len(disconnectOptions) {
		m.switching.OnDisconnect = disconnectOptions[idx].value
	}
}

func (m *configModel) clampCursors() {
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

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
