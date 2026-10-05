package daemon

import (
	"fmt"
	"log"
	"os"
	"slices"
	"time"

	"github.com/roverflow/poweraudio/internal/ipc"
)

// The in-memory ring keeps every level for the status screen.
// general.log_level filters only stderr and the log file.

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
	d.events = appendEvent(d.events, entry)
	threshold := d.cfg.General.LogLevel
	d.mu.Unlock()

	if levelRank(level) >= levelRank(parseLevel(threshold)) {
		d.write(entry)
	}
	d.changed()
}

// appendEvent drops the oldest debug line past maxDebugEvents, not the oldest
// line overall.
func appendEvent(events []ipc.EventLog, entry ipc.EventLog) []ipc.EventLog {
	events = append(events, entry)
	if entry.Level == ipc.LevelDebug {
		debug, oldest := 0, -1
		for i, ev := range events {
			if ev.Level == ipc.LevelDebug {
				if oldest < 0 {
					oldest = i
				}
				debug++
			}
		}
		if debug > maxDebugEvents {
			events = slices.Delete(events, oldest, oldest+1)
		}
	}
	if len(events) > maxEvents {
		events = events[len(events)-maxEvents:]
	}
	return events
}

func (d *Daemon) write(entry ipc.EventLog) {
	d.logMu.Lock()
	defer d.logMu.Unlock()

	log.Printf("[%s] %s", entry.Level, entry.Message)
	if d.logFile != nil {
		fmt.Fprintf(d.logFile, "%s [%s] %s\n",
			entry.Time.Format(time.RFC3339), entry.Level, entry.Message)
	}
}

// setLogFile opens path for append. An empty path turns the file off.
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

// parseLevel treats anything unrecognised, including empty, as info.
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
