package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/priority"
)

// deviceTypeW fits "Bluetooth" and "Headphone", the longest type labels.
const deviceTypeW = 9

const (
	// deviceHeaderRows is the title, the current-output line and a blank.
	deviceHeaderRows = 3

	// devicePanelRows is the detail panel: a rule line and five fields.
	devicePanelRows = 6
)

type devicesModel struct {
	devices []audio.Device
	entries []config.PriorityEntry
	ready   bool

	cursor int
	offset int

	width  int
	height int

	vol volumeState
}

func newDevicesModel() devicesModel {
	return devicesModel{width: defaultWidth, height: defaultHeight - chromeLines}
}

func (m devicesModel) Update(msg tea.Msg) (devicesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m devicesModel) handleKey(msg tea.KeyPressMsg) (devicesModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.devices)-1 {
			m.cursor++
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		if len(m.devices) > 0 {
			m.cursor = len(m.devices) - 1
		}
	case "pgdown", "ctrl+f":
		m.moveCursor(m.rowCount())
	case "pgup", "ctrl+b":
		m.moveCursor(-m.rowCount())
	case "enter":
		if m.cursor < len(m.devices) {
			cmd = requestDefaultCmd(m.devices[m.cursor].ID)
		}
	case "right", "l":
		m, cmd = m.stepVolume(volumeStep)
	case "left", "h":
		m, cmd = m.stepVolume(-volumeStep)
	case "L", "shift+right":
		m, cmd = m.stepVolume(volumeFine)
	case "H", "shift+left":
		m, cmd = m.stepVolume(-volumeFine)
	case "m":
		if m.cursor < len(m.devices) {
			cmd = requestMuteCmd(m.devices[m.cursor].ID)
		}
	}

	m.syncScroll()
	return m, cmd
}

func (m *devicesModel) moveCursor(delta int) {
	m.cursor += delta
	if m.cursor > len(m.devices)-1 {
		m.cursor = len(m.devices) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *devicesModel) setSnapshot(devices []audio.Device, entries []config.PriorityEntry) {
	m.devices = devices
	m.entries = entries
	m.ready = true
	if m.cursor >= len(m.devices) {
		m.cursor = max(0, len(m.devices)-1)
	}
	m.vol.snapshot()
	m.syncScroll()
}

func (m devicesModel) stepVolume(delta int) (devicesModel, tea.Cmd) {
	if m.cursor >= len(m.devices) {
		return m, nil
	}
	dev := m.devices[m.cursor]
	next := clampVolume(m.volumeOf(dev) + delta)

	switch m.vol.edit(dev.ID, next, time.Now()) {
	case volumeArm:
		return m, volumeTickCmd(m.vol.seq)
	case volumeSend:
		return m, requestVolumeCmd(m.vol.id, m.vol.percent)
	}
	return m, nil
}

func (m devicesModel) volumeTick(seq int) (devicesModel, tea.Cmd) {
	if m.vol.fire(seq) == volumeSend {
		return m, requestVolumeCmd(m.vol.id, m.vol.percent)
	}
	return m, nil
}

func (m devicesModel) volumeDone() (devicesModel, tea.Cmd) {
	if m.vol.done() == volumeArm {
		return m, volumeTickCmd(m.vol.seq)
	}
	return m, nil
}

func (m devicesModel) volumeOf(dev audio.Device) int {
	if pct, ok := m.vol.level(dev.ID, time.Now()); ok {
		return pct
	}
	return int(math.Round(dev.Volume * 100))
}

func (m devicesModel) click(row int) (devicesModel, tea.Cmd) {
	idx := m.rowIndex(row)
	if idx < 0 {
		return m, nil
	}
	if idx == m.cursor {
		return m, requestDefaultCmd(m.devices[idx].ID)
	}
	m.cursor = idx
	m.syncScroll()
	return m, nil
}

func (m devicesModel) rowIndex(row int) int {
	row -= deviceHeaderRows
	if row < 0 || row >= m.rowCount() {
		return -1
	}
	idx := m.offset + row
	if idx >= len(m.devices) {
		return -1
	}
	return idx
}

// scroll moves the cursor so the selection stays on screen.
func (m devicesModel) scroll(delta int) (devicesModel, tea.Cmd) {
	m.moveCursor(delta)
	m.syncScroll()
	return m, nil
}

func (m *devicesModel) syncScroll() {
	m.offset = clampOffset(m.offset, m.cursor, len(m.devices), m.rowCount())
}

func (m devicesModel) rowCount() int {
	_, h := screenSize(m.width, m.height)
	n := h - deviceHeaderRows - m.footerRows()
	if n < 1 {
		return 1
	}
	return n
}

func (m devicesModel) footerRows() int {
	if m.showPanel() {
		return 2 + devicePanelRows
	}
	return 2
}

func (m devicesModel) showPanel() bool {
	_, h := screenSize(m.width, m.height)
	return devicePanelFits(h, len(m.devices))
}

func devicePanelFits(height, devices int) bool {
	if devices == 0 {
		return false
	}
	spare := height - deviceHeaderRows - 2 - devices
	return spare >= devicePanelRows
}

func deviceColumns(width int) (nameW, typeW, barW int) {
	// The row spends nine columns on the marker, the gaps and the percentage.
	avail := width - 3 - 9

	typeW, barW = deviceTypeW, 14
	if avail < 48 {
		barW = 8
	}
	if avail < 34 {
		typeW = 0
	}
	if avail < 24 {
		barW = 5
	}

	typeBlock := 0
	if typeW > 0 {
		typeBlock = typeW + 2
	}

	nameW = avail - typeBlock - barW
	if nameW < 8 {
		nameW = 8
		barW = avail - typeBlock - nameW
		if barW < 3 {
			barW = 3
		}
	}
	if nameW+typeBlock+barW > avail {
		nameW = avail - typeBlock - barW
	}
	if nameW < 1 {
		nameW = 1
	}
	return nameW, typeW, barW
}

func (m devicesModel) View() string {
	w, h := screenSize(m.width, m.height)

	if !m.ready {
		return frame(h,
			[]string{"  " + styleTitle.Render("Audio Output Devices"), ""},
			[]string{
				"  " + styleMuted.Render(truncate("Waiting for the daemon. Start one with: poweraudio --daemon", w-3)),
			},
			[]string{"", helpLine(w, "r reconnect", "? help", "q quit")},
		)
	}

	visible := m.rowCount()
	nameW, typeW, barW := deviceColumns(w)

	title := "  " + styleTitle.Render("Audio Output Devices")
	if hint := scrollHint(m.offset, visible, len(m.devices)); hint != "" {
		title += styleMuted.Render(fmt.Sprintf("   %s  %d/%d", hint, m.cursor+1, len(m.devices)))
	}

	current := "none"
	for _, d := range m.devices {
		if d.IsDefault {
			current = d.Name
			break
		}
	}

	header := []string{
		title,
		"  " + styleMuted.Render("playing through ") + styleActive.Render(truncate(current, w-20)),
		"",
	}

	var body []string
	if len(m.devices) == 0 {
		body = []string{"  " + styleMuted.Render("No audio devices found")}
	} else {
		rows := make([]string, 0, len(m.devices))
		for i, dev := range m.devices {
			rows = append(rows, m.row(i, dev, w, nameW, typeW, barW))
		}
		body = window(rows, m.offset, visible)
	}

	footer := []string{}
	if m.showPanel() {
		footer = append(footer, "  "+styleMuted.Render(strings.Repeat("─", max(w-4, 1))))
		footer = append(footer, detailRows(m.devices[m.cursor], m.volumeOf(m.devices[m.cursor]), m.entries, w)...)
	}
	footer = append(footer, "",
		helpLine(w, "enter set default", "←→ volume", "m mute", "j/k move", "? help", "q quit"))

	return frame(h, header, body, footer)
}

func (m devicesModel) row(i int, dev audio.Device, width, nameW, typeW, barW int) string {
	selected := i == m.cursor
	vol := m.volumeOf(dev)

	pieces := []rowPiece{}

	if dev.IsDefault {
		pieces = append(pieces, keepPiece("●", styleActive, colorSuccess))
	} else {
		pieces = append(pieces, piece(" ", styleNormal))
	}

	nameStyle := styleNormal
	if dev.IsDefault {
		nameStyle = styleActive
	}
	pieces = append(pieces, piece(" "+fit(dev.Name, nameW), nameStyle))

	if typeW > 0 {
		pieces = append(pieces, piece("  "+fit(dev.Type.String(), typeW), styleMuted))
	}

	pieces = append(pieces, piece("  ", styleNormal))
	pieces = append(pieces, volumePieces(vol, dev.Muted, barW)...)

	label := fmt.Sprintf("%3d%%", vol)
	if dev.Muted {
		label = "MUTE"
	}
	pieces = append(pieces, piece(" "+label, styleMuted))

	return listRow(width, selected, pieces...)
}

// volumePieces turns amber above 100%, where PipeWire amplifies and can
// clip.
func volumePieces(percent int, muted bool, width int) []rowPiece {
	if width < 1 {
		width = 1
	}
	if muted {
		return []rowPiece{keepPiece(strings.Repeat("─", width), styleVolumeOff, colorMuted)}
	}

	filled := int(math.Round(float64(percent) / 100 * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	on, onColor := styleVolumeOn, colorSuccess
	if percent > 100 {
		on, onColor = styleVolumeLoud, colorWarning
	}
	return []rowPiece{
		keepPiece(strings.Repeat("━", filled), on, onColor),
		piece(strings.Repeat("─", width-filled), styleVolumeOff),
	}
}

func detailRows(dev audio.Device, percent int, entries []config.PriorityEntry, width int) []string {
	valueW := width - 14
	if valueW < 1 {
		valueW = 1
	}

	volume := fmt.Sprintf("%d%%", percent)
	if dev.Muted {
		volume += "  ·  muted"
	}

	def := "no"
	if dev.IsDefault {
		def = "yes"
	}
	// Shares the default row so the panel stays devicePanelRows tall.
	if !dev.Available {
		def += "  ·  unavailable"
	}

	mac := dev.MACAddress
	if mac == "" {
		mac = "none"
	}

	return []string{
		field("Sink", styleMuted.Render(truncate(dev.Description, valueW))),
		field("Type", styleNormal.Render(truncate(dev.Type.String(), valueW))),
		field("MAC", styleMuted.Render(truncate(mac, valueW))),
		field("Volume", styleNormal.Render(truncate(volume, valueW))),
		field("Priority", styleNormal.Render(truncate(rankLabel(dev, entries)+"  ·  default "+def, valueW))),
	}
}

func rankLabel(dev audio.Device, entries []config.PriorityEntry) string {
	rank := priority.Rank(dev, entries)
	if rank == len(entries) {
		return "not on the priority list"
	}
	return fmt.Sprintf("ranked %d of %d", rank+1, len(entries))
}

func field(label, value string) string {
	return "  " + styleMuted.Render(fit(label, 10)) + value
}
