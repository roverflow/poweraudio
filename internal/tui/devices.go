package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/audio"
)

// deviceTypeW is the width of the device-type column. "Bluetooth" and
// "Headphone" are the longest labels DeviceType.String returns.
const deviceTypeW = 9

// volEditHold is how long a local volume edit wins over the value coming back
// from the daemon. Without it the two second refresh tick overwrites the bar
// mid-keypress and the level appears to jump backwards.
const volEditHold = 800 * time.Millisecond

type DevicesModel struct {
	devices []audio.Device
	cursor  int
	offset  int
	err     error

	width  int
	height int

	volEditID  string
	volEditVal float64
	volEditAt  time.Time
}

func NewDevicesModel() DevicesModel {
	return DevicesModel{width: defaultWidth, height: defaultHeight - chromeLines}
}

func (m DevicesModel) Update(msg tea.Msg) (DevicesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
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
			m.cursor += m.rowCount()
			if m.cursor > len(m.devices)-1 {
				m.cursor = len(m.devices) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "pgup", "ctrl+b":
			m.cursor -= m.rowCount()
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "enter":
			if m.cursor < len(m.devices) {
				return m, setDefaultCmd(m.devices[m.cursor].ID)
			}
		case "right", "l":
			if m.cursor < len(m.devices) {
				return m.stepVolume(5)
			}
		case "left", "h":
			if m.cursor < len(m.devices) {
				return m.stepVolume(-5)
			}
		case "m":
			if m.cursor < len(m.devices) {
				return m, muteCmd(m.devices[m.cursor].ID)
			}
		}
		m.syncScroll()

	case devicesMsg:
		m.devices = msg.devices
		m.err = msg.err
		if m.cursor >= len(m.devices) {
			m.cursor = max(0, len(m.devices)-1)
		}
		m.keepPendingVolume()
		m.syncScroll()
	}
	return m, nil
}

// stepVolume applies the change locally first so the bar tracks the key, then
// asks the daemon to match. keepPendingVolume defends the local value against
// the refresh that follows.
func (m DevicesModel) stepVolume(delta int) (DevicesModel, tea.Cmd) {
	dev := m.devices[m.cursor]

	next := int(math.Round(dev.Volume*100)) + delta
	if next > 150 {
		next = 150
	}
	if next < 0 {
		next = 0
	}

	m.devices[m.cursor].Volume = float64(next) / 100.0
	m.volEditID = dev.ID
	m.volEditVal = float64(next) / 100.0
	m.volEditAt = time.Now()

	return m, volumeCmd(dev.ID, next)
}

func (m *DevicesModel) keepPendingVolume() {
	if m.volEditID == "" || time.Since(m.volEditAt) > volEditHold {
		m.volEditID = ""
		return
	}
	for i := range m.devices {
		if m.devices[i].ID == m.volEditID {
			m.devices[i].Volume = m.volEditVal
			return
		}
	}
}

func (m *DevicesModel) syncScroll() {
	m.offset = clampOffset(m.offset, m.cursor, len(m.devices), m.rowCount())
}

// rowCount is the number of device rows that fit: the height budget less the
// title, the current-output line, a blank, a blank and the help line.
func (m DevicesModel) rowCount() int {
	if n := m.height - 5; n > 0 {
		return n
	}
	return 1
}

// columns splits the terminal width between the name and the volume bar. The
// fixed part covers the cursor gutter, the default marker, the type column,
// the gaps between them and the percentage.
func (m DevicesModel) columns() (nameW, barW int) {
	const fixed = 2 + 1 + 1 + 2 + deviceTypeW + 2 + 1 + 4

	w := m.width
	if w < minWidth {
		w = minWidth
	}
	// Leave the last column empty. A row that fills the terminal exactly can
	// trip auto-wrap on the final cell, which would add a phantom line and
	// push the whole frame off by one.
	w--

	barW = 14
	nameW = w - fixed - barW
	if nameW < 16 {
		barW = 8
		nameW = w - fixed - barW
	}
	if nameW < 10 {
		// Out of room. Hold the name at its floor and let the bar shrink.
		nameW = 10
		barW = w - fixed - nameW
		if barW < 4 {
			barW = 4
		}
	}
	return nameW, barW
}

func (m DevicesModel) View() string {
	w := m.width
	if w < minWidth {
		w = minWidth
	}
	h := m.height
	if h < minContentH {
		h = minContentH
	}

	if m.err != nil {
		return frame(h,
			[]string{"  " + styleTitle.Render("Audio Output Devices"), ""},
			[]string{
				"  " + styleError.Render(truncate(m.err.Error(), w-2)),
				"",
				"  " + styleMuted.Render(truncate("The daemon is not answering. Start it with: poweraudio --daemon", w-2)),
			},
			[]string{"", helpLine(w, "r retry", "q quit")},
		)
	}

	visible := m.rowCount()
	nameW, barW := m.columns()

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
			rows = append(rows, m.row(i, dev, nameW, barW))
		}
		body = window(rows, m.offset, visible)
	}

	footer := []string{
		"",
		helpLine(w, "enter set default", "←→ volume", "m mute", "j/k move", "r refresh", "? help"),
	}

	return frame(h, header, body, footer)
}

func (m DevicesModel) row(i int, dev audio.Device, nameW, barW int) string {
	selected := i == m.cursor

	prefix := "  "
	if selected {
		prefix = styleAccent.Render("▎") + " "
	}

	marker := " "
	if dev.IsDefault {
		marker = styleActive.Render("●")
	}

	text := fit(dev.Name, nameW) + "  " + fit(dev.Type.String(), deviceTypeW)
	switch {
	case selected:
		text = styleSelected.Render(text)
	case dev.IsDefault:
		text = styleActive.Render(text)
	default:
		text = styleNormal.Render(text)
	}

	label := fmt.Sprintf("%3.0f%%", dev.Volume*100)
	if dev.Muted {
		label = "MUTE"
	}

	return prefix + marker + " " + text + "  " +
		renderVolume(dev.Volume, dev.Muted, barW) + " " + styleMuted.Render(label)
}

// renderVolume draws the level as a filled run of heavy rule against a light
// one. Anything above 100% turns amber, since PipeWire will happily amplify
// past unity and clip.
func renderVolume(vol float64, muted bool, width int) string {
	if width < 1 {
		width = 1
	}
	if muted {
		return styleVolumeOff.Render(strings.Repeat("─", width))
	}

	filled := int(math.Round(vol * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	on := styleVolumeOn
	if vol > 1.0 {
		on = styleVolumeLoud
	}
	return on.Render(strings.Repeat("━", filled)) +
		styleVolumeOff.Render(strings.Repeat("─", width-filled))
}

type devicesMsg struct {
	devices []audio.Device
	err     error
}

type setDefaultMsg struct {
	err error
}

func setDefaultCmd(deviceID string) tea.Cmd {
	return func() tea.Msg {
		return switchDeviceMsg{deviceID: deviceID}
	}
}

type switchDeviceMsg struct {
	deviceID string
}

type volumeMsg struct {
	deviceID string
	percent  int
}

type muteMsg struct {
	deviceID string
}

type volumeResultMsg struct {
	err error
}

type muteResultMsg struct {
	err error
}

func volumeCmd(deviceID string, percent int) tea.Cmd {
	return func() tea.Msg {
		return volumeMsg{deviceID: deviceID, percent: percent}
	}
}

func muteCmd(deviceID string) tea.Cmd {
	return func() tea.Msg {
		return muteMsg{deviceID: deviceID}
	}
}
