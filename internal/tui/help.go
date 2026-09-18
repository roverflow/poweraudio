package tui

import tea "charm.land/bubbletea/v2"

const (
	// helpChrome is the title line and the help line the help screen spends
	// on itself, which is what is left over for the key rows.
	helpChrome = 2

	// helpKeyW is the key column, wide enough that the longest combination
	// still leaves a gap before its description.
	helpKeyW = 11
)

type helpModel struct {
	offset int

	width  int
	height int
}

func newHelpModel() helpModel {
	return helpModel{width: defaultWidth, height: defaultHeight - chromeLines}
}

func (m helpModel) Update(msg tea.Msg) (helpModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "down", "j":
			return m.scroll(1)
		case "up", "k":
			return m.scroll(-1)
		case "g", "home":
			m.offset = 0
		case "G", "end":
			m.offset = clampScroll(len(helpRows), len(helpRows), m.rowCount())
		}
	}
	return m, nil
}

// scroll walks the reference, which no longer fits a twenty-four row terminal
// now that it documents the mouse and the fine volume steps.
func (m helpModel) scroll(delta int) (helpModel, tea.Cmd) {
	m.offset = clampScroll(m.offset+delta, len(helpRows), m.rowCount())
	return m, nil
}

func (m helpModel) rowCount() int {
	_, h := screenSize(m.width, m.height)
	if n := h - helpChrome; n > 0 {
		return n
	}
	return 1
}

var helpRows = []struct{ key, desc string }{
	{"Anywhere", ""},
	{"d c s", "devices, config, status, also 1 2 3"},
	{"? r q", "help, refresh or reconnect, quit"},
	{"mouse", "click a tab or a row, wheel scrolls the list"},
	{"", ""},
	{"Devices", ""},
	{"j k enter", "move and make the selection the default output"},
	{"h l", "volume in 5% steps, H L in 1% steps"},
	{"m", "mute or unmute"},
	{"", ""},
	{"Config", ""},
	{"tab", "swap between priorities and switching rules"},
	{"j k J K", "move, reorder the selected entry"},
	{"enter x w", "add or play a device, drop an entry, save"},
	{"", ""},
	{"Status", ""},
	{"j k i u", "scroll the log, install or remove the service"},
}

func (m helpModel) View() string {
	w, h := screenSize(m.width, m.height)
	visible := m.rowCount()

	title := "  " + styleTitle.Render("Keys")
	if hint := scrollHint(m.offset, visible, len(helpRows)); hint != "" {
		title += styleMuted.Render("   " + hint)
	}

	body := make([]string, 0, len(helpRows))
	for _, r := range helpRows {
		switch {
		case r.key == "":
			body = append(body, "")
		case r.desc == "":
			body = append(body, "  "+styleSubtitle.Render(r.key))
		default:
			body = append(body, "  "+styleKey.Render(fit(r.key, helpKeyW))+
				styleMuted.Render(truncate(r.desc, w-helpKeyW-3)))
		}
	}

	return frame(h,
		[]string{title},
		window(body, m.offset, visible),
		[]string{helpLine(w, "j/k scroll", "? or esc closes", "q quit")},
	)
}
