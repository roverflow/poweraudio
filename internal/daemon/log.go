package daemon

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/roverflow/poweraudio/internal/ipc"
)

// The in-memory ring keeps every level so the status screen can show what the
// daemon was doing. general.log_level only decides what reaches stderr, which
// systemd captures, and the optional log file.

func (d *Daemon) debugf(format string, args ...any) { d.logf(ipc.LevelDebug, format, args...) }
func (d *Daemon) infof(format string, args ...any)  { d.logf(ipc.LevelInfo, format, args...) }
func (d *Daemon) warnf(format string, args ...any)  { d.logf(ipc.LevelWarn, format, args...) }
func (d *Daemon) errorf(format string, args ...any) { d.logf(ipc.LevelError, format, args...) }

func (d *Daemon) logf(level ipc.Level, format string, args ...any) {
	entry := ipc.EventLog{
		Time:    time.Now(),
		Level:   level,
		Message: fmt.Sprintf(format, args...),
	}

	d.mu.Lock()
	d.events = append(d.events, entry)
	if len(d.events) > maxEvents {
		d.events = d.events[len(d.events)-maxEvents:]
	}
	threshold := d.cfg.General.LogLevel
	d.mu.Unlock()

	if levelRank(level) >= levelRank(parseLevel(threshold)) {
		d.write(entry)
	}
	d.changed()
}

// write puts one line on stderr and, when general.log_file is set, the same
// line in that file. The lock keeps two goroutines from interleaving halves of
// a line in the file.
func (d *Daemon) write(entry ipc.EventLog) {
	d.logMu.Lock()
	defer d.logMu.Unlock()

	log.Printf("[%s] %s", entry.Level, entry.Message)
	if d.logFile != nil {
		fmt.Fprintf(d.logFile, "%s [%s] %s\n",
			entry.Time.Format(time.RFC3339), entry.Level, entry.Message)
	}
}

// setLogFile points the file half of the log at path, opening it for append so
// a restart does not truncate yesterday's lines. An empty path turns it off.
// A reload can move the destination, so this is not only a startup step.
func (d *Daemon) setLogFile(path string) {
	d.logMu.Lock()
	unchanged := path == d.logPath
	d.logMu.Unlock()
	if unchanged {
		return
	}

	var f *os.File
	if path != "" {
		opened, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			d.errorf("opening log file %s: %v", path, err)
			return
		}
		f = opened
	}

	d.logMu.Lock()
	if d.logFile != nil {
		d.logFile.Close()
	}
	d.logFile = f
	d.logPath = path
	d.logMu.Unlock()
}

func (d *Daemon) closeLogFile() {
	d.logMu.Lock()
	defer d.logMu.Unlock()
	if d.logFile != nil {
		d.logFile.Close()
		d.logFile = nil
	}
	d.logPath = ""
}

// parseLevel reads general.log_level. Anything unrecognised, including the
// empty string a config written before levels existed leaves behind, means
// info.
func parseLevel(s string) ipc.Level {
	switch ipc.Level(s) {
	case ipc.LevelDebug, ipc.LevelInfo, ipc.LevelWarn, ipc.LevelError:
		return ipc.Level(s)
	default:
		return ipc.LevelInfo
	}
}

func levelRank(l ipc.Level) int {
	switch l {
	case ipc.LevelDebug:
		return 0
	case ipc.LevelWarn:
		return 2
	case ipc.LevelError:
		return 3
	default:
		return 1
	}
}
