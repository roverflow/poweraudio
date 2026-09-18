package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/ipc"
)

const (
	// eventTime is short because the interesting part of a switch is the
	// second it happened on, not the date.
	eventTime = "Jan 02 15:04:05"

	// statusEvents is how much of the daemon's log the status command shows.
	// The rest is what the terminal UI and journalctl are for.
	statusEvents = 10
)

// renderList is the device table: a marker on the default, then name, type,
// level and the id you would pass back to "set".
func renderList(snap *ipc.Snapshot) string {
	if len(snap.Devices) == 0 {
		return "no output devices\n"
	}

	var buf bytes.Buffer
	tw := newTable(&buf)
	for _, dev := range snap.Devices {
		marker := " "
		if dev.IsDefault {
			marker = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", marker, dev.Name, dev.Type, volumeLabel(dev), dev.ID)
	}
	tw.Flush()
	return buf.String()
}

// renderStatus is the daemon at a glance followed by the tail of its log. now
// is a parameter rather than a call to time.Now so the uptime line is worth
// testing.
func renderStatus(snap *ipc.Snapshot, now time.Time) string {
	var buf bytes.Buffer

	tw := newTable(&buf)
	if def := snap.Default(); def != nil {
		fmt.Fprintf(tw, "default\t%s  %s\n", def.Name, volumeLabel(*def))
	} else {
		fmt.Fprintf(tw, "default\tnone\n")
	}
	fmt.Fprintf(tw, "backend\t%s\n", snap.Status.Backend)
	fmt.Fprintf(tw, "config\t%s\n", snap.Status.ConfigPath)
	fmt.Fprintf(tw, "uptime\t%s\n", uptime(snap.Status.StartedAt, now))
	tw.Flush()

	events := tailEvents(snap.Events, statusEvents)
	if len(events) == 0 {
		return buf.String()
	}

	buf.WriteString("\nevents\n")
	etw := newTable(&buf)
	for _, event := range events {
		fmt.Fprintf(etw, "%s\t%s\t%s\n", event.Time.Format(eventTime), event.Level, event.Message)
	}
	etw.Flush()
	return buf.String()
}

// watchLine is one snapshot reduced to what a status bar draws.
func watchLine(snap ipc.Snapshot) string {
	def := snap.Default()
	if def == nil {
		return "none"
	}
	return fmt.Sprintf("%s  %s", def.Name, volumeLabel(*def))
}

func volumeLabel(dev audio.Device) string {
	if dev.Muted {
		return "muted"
	}
	return fmt.Sprintf("%d%%", volumePercent(dev.Volume))
}

// tailEvents is the last n events, oldest first, so the newest line is the
// one nearest the prompt.
func tailEvents(events []ipc.EventLog, n int) []ipc.EventLog {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

// uptime is how long the daemon has been up, in the largest two units that
// say anything. A daemon that has not reported a start time reads as unknown
// rather than as decades of uptime.
func uptime(startedAt, now time.Time) string {
	if startedAt.IsZero() {
		return "unknown"
	}

	d := now.Sub(startedAt).Round(time.Second)
	if d < time.Second {
		return "0s"
	}

	days := int(d / (24 * time.Hour))
	hours := int(d/time.Hour) % 24
	minutes := int(d/time.Minute) % 60
	seconds := int(d/time.Second) % 60

	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

func newTable(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// writeJSON prints a value for another program to read. Indented because the
// commands that use it print one document and exit, unlike watch.
func writeJSON(out io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding json: %w", err)
	}
	_, err = fmt.Fprintf(out, "%s\n", data)
	return err
}
