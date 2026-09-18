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

// deviceTypeW is the width of the device-type column. "Bluetooth" and
// "Headphone" are the longest labels DeviceType.String returns.
const deviceTypeW = 9

const (
	// deviceHeaderRows is the title, the current-output line and a blank.
	deviceHeaderRows = 3

	// devicePanelRows is the detail panel: a blank separator and five fields.
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

// setSnapshot takes the device list from a pushed snapshot. A volume edit that
// has not been confirmed yet keeps its local level, so a snapshot that crosses
// a keypress in flight cannot drag the bar backwards.
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

// stepVolume applies the change locally first so the bar tracks the key, then
// hands the coalescing decision to volumeState.
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

// volumeTick runs the debounce timer to its end.
func (m devicesModel) volumeTick(seq int) (devicesModel, tea.Cmd) {
	if m.vol.fire(seq) == volumeSend {
		return m, requestVolumeCmd(m.vol.id, m.vol.percent)
	}
	return m, nil
}

// volumeDone closes one request out and starts the next one if the level moved
// while it was out.
func (m devicesModel) volumeDone() (devicesModel, tea.Cmd) {
	if m.vol.done() == volumeArm {
		return m, volumeTickCmd(m.vol.seq)
	}
	return m, nil
}

// volumeOf is the level to draw, which is the pending local edit when there is
// one and the daemon's value otherwise.
func (m devicesModel) volumeOf(dev audio.Device) int {
	if pct, ok := m.vol.level(dev.ID, time.Now()); ok {
		return pct
	}
	return int(math.Round(dev.Volume * 100))
}

// click moves the cursor to the row under the pointer. Clicking the row that
// is already selected is the second half of a click-to-play gesture and sets
// the device as the default output.
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

// rowIndex maps a row of the content area onto a device, or -1 when the
// pointer is on the header, the detail panel or empty space.
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

// scroll moves the cursor rather than the window alone, so the selection stays
// on screen and the next keypress carries on from what the pointer picked.
func (m devicesModel) scroll(delta int) (devicesModel, tea.Cmd) {
	m.moveCursor(delta)
	m.syncScroll()
	return m, nil
}

func (m *devicesModel) syncScroll() {
	m.offset = clampOffset(m.offset, m.cursor, len(m.devices), m.rowCount())
}

// rowCount is the number of device rows that fit once the header, the help
// line and, when it is drawn, the detail panel have taken their rows.
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

// showPanel keeps the list and drops the panel first on a short terminal,
// because a device you cannot see is worse than one you cannot inspect.
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

// deviceColumns splits the width between the name, the type and the volume
// bar. The type column goes first on a narrow terminal and the bar shrinks
// after it; below roughly twenty columns everything clips.
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
		// A rule rather than a blank, because the panel is anchored to the
		// bottom of the screen and a short list leaves a gap above it.
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

// volumePieces draws the level as a filled run of heavy rule against a light
// one. Anything above 100% turns amber, since PipeWire will happily amplify
// past unity and clip.
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

// detailRows describes the selected device below the list: the technical sink
// name the daemon and pactl use, and the facts that are not in the row.
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

// rankLabel places a device on the priority list using the same matcher the
// daemon switches with, so the panel cannot claim a rank the daemon disagrees
// with.
func rankLabel(dev audio.Device, entries []config.PriorityEntry) string {
	rank := priority.Rank(dev, entries)
	if rank == len(entries) {
		return "not on the priority list"
	}
	return fmt.Sprintf("ranked %d of %d", rank+1, len(entries))
}

// field lays a label and a value out in two columns.
func field(label, value string) string {
	return "  " + styleMuted.Render(fit(label, 10)) + value
}
