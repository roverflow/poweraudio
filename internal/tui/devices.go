package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/audio"
)

// #region agent log
func debugLog(location, message string, data map[string]any) {
	entry := map[string]any{
		"sessionId":    "cb8da3",
		"location":     location,
		"message":      message,
		"data":         data,
		"timestamp":    time.Now().UnixMilli(),
	}
	raw, _ := json.Marshal(entry)
	raw = append(raw, '\n')
	f, err := os.OpenFile("/home/roverflow/workspace/projects/poweraudio/.cursor/debug-cb8da3.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		f.Write(raw)
		f.Close()
	}
}
// #endregion

type DevicesModel struct {
	devices  []audio.Device
	cursor   int
	err      error
	width    int
}

func NewDevicesModel() DevicesModel {
	return DevicesModel{}
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
		case "enter":
			if m.cursor < len(m.devices) {
				return m, setDefaultCmd(m.devices[m.cursor].ID)
			}
		case "right", "l":
			if m.cursor < len(m.devices) {
				dev := m.devices[m.cursor]
				newVol := int(dev.Volume*100) + 5
				if newVol > 150 {
					newVol = 150
				}
				m.devices[m.cursor].Volume = float64(newVol) / 100.0
				return m, volumeCmd(dev.ID, newVol)
			}
		case "left", "h":
			if m.cursor < len(m.devices) {
				dev := m.devices[m.cursor]
				// #region agent log
				debugLog("devices.go:left-press", "volume decrease requested", map[string]any{
					"hypothesisId": "H1_H5",
					"deviceID":     dev.ID,
					"rawVolume":    dev.Volume,
					"vol_times_100": dev.Volume * 100,
					"int_vol100":   int(dev.Volume * 100),
				})
				// #endregion
				newVol := int(dev.Volume*100) - 5
				if newVol < 0 {
					newVol = 0
				}
				m.devices[m.cursor].Volume = float64(newVol) / 100.0
				// #region agent log
				debugLog("devices.go:left-press-result", "volume decrease computed", map[string]any{
					"hypothesisId":  "H1",
					"newVol":        newVol,
					"newLocalVol":   m.devices[m.cursor].Volume,
					"deviceID":      dev.ID,
				})
				// #endregion
				return m, volumeCmd(dev.ID, newVol)
			}
		case "m":
			if m.cursor < len(m.devices) {
				return m, muteCmd(m.devices[m.cursor].ID)
			}
		}
	case devicesMsg:
		// #region agent log
		var volSnap []map[string]any
		for _, d := range msg.devices {
			volSnap = append(volSnap, map[string]any{"id": d.ID, "vol": d.Volume, "name": d.Name})
		}
		debugLog("devices.go:devicesMsg", "devices refreshed from daemon", map[string]any{
			"hypothesisId": "H2_H5",
			"deviceCount":  len(msg.devices),
			"volumes":      volSnap,
		})
		// #endregion
		m.devices = msg.devices
		m.err = msg.err
		if m.cursor >= len(m.devices) && len(m.devices) > 0 {
			m.cursor = len(m.devices) - 1
		}
	}
	return m, nil
}

func (m DevicesModel) View() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("Audio Output Devices"))
	b.WriteString("\n")

	if m.err != nil {
		b.WriteString(styleMuted.Render(fmt.Sprintf("Error: %v", m.err)))
		return b.String()
	}

	if len(m.devices) == 0 {
		b.WriteString(styleMuted.Render("No audio devices found"))
		return b.String()
	}

	maxName := 0
	for _, d := range m.devices {
		if len(d.Name) > maxName {
			maxName = len(d.Name)
		}
	}
	if maxName > 40 {
		maxName = 40
	}

	for i, dev := range m.devices {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		marker := " "
		if dev.IsDefault {
			marker = "*"
		}

		name := dev.Name
		if len(name) > maxName {
			name = name[:maxName-1] + "~"
		}

		volBar := renderVolume(dev.Volume, dev.Muted, 10)
		volPct := fmt.Sprintf("%3.0f%%", dev.Volume*100)
		if dev.Muted {
			volPct = "MUTE"
		}

		typeName := fmt.Sprintf("%-10s", dev.Type.String())

		line := fmt.Sprintf("%s%s %-*s  %s  %s %s",
			cursor, marker, maxName, name, typeName, volBar, volPct)

		if i == m.cursor {
			b.WriteString(styleSelected.Render(line))
		} else if dev.IsDefault {
			b.WriteString(styleActive.Render(line))
		} else {
			b.WriteString(styleNormal.Render(line))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styleHelp.Render("[Enter] Set default  [j/k] Navigate  [←/→] Volume  [m] Mute  [r] Refresh"))

	return b.String()
}

func renderVolume(vol float64, muted bool, width int) string {
	if muted {
		return styleVolumeOff.Render(strings.Repeat("░", width))
	}

	filled := int(vol * float64(width))
	if filled > width {
		filled = width
	}

	on := styleVolumeOn.Render(strings.Repeat("█", filled))
	off := styleVolumeOff.Render(strings.Repeat("░", width-filled))
	return on + off
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
