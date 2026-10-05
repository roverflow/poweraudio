// Package razer reads the Barracuda X dongle's earcup report. The dongle
// sends one HID report per power change and nothing in between, so SaveState
// keeps the last one for a restarted daemon.
package razer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
)

const (
	VendorID  = 0x1532
	ProductID = 0x054e

	hidID = "0003:00001532:0000054E"

	reportID = 0x02

	// connectedByte is 0x01 with the earcups on and 0x00 off. In a capture on
	// 2026-09-22 it was the only byte of the report that followed the switch.
	connectedByte = 13
)

// IsBarracuda matches the receiver by USB id, or by sink name without ids.
func IsBarracuda(dev audio.Device) bool {
	if dev.VendorID == VendorID && dev.ProductID == ProductID {
		return true
	}
	return strings.Contains(dev.ID, "usb-1532_Razer_Barracuda_X")
}

// ParseReport reads one hidraw buffer. ok is false for anything but report
// 0x02 with an on or off byte.
func ParseReport(b []byte) (on bool, ok bool) {
	if len(b) <= connectedByte || b[0] != reportID {
		return false, false
	}
	switch b[connectedByte] {
	case 0x00:
		return false, true
	case 0x01:
		return true, true
	default:
		return false, false
	}
}

// Event is one watcher result. On only means something when Err is nil and
// Opened is false.
type Event struct {
	On     bool
	Opened bool
	Path   string
	Err    error
}

// Watch reads the dongle until ctx ends. A replug can rename the node, so a
// failed read starts the search again. A repeated error is sent only once.
func Watch(ctx context.Context) <-chan Event {
	ch := make(chan Event, 4)
	go func() {
		defer close(ch)
		var lastErr string
		for {
			if ctx.Err() != nil {
				return
			}
			path, err := FindHidraw()
			if err != nil {
				if !sleep(ctx, 2*time.Second) {
					return
				}
				continue
			}
			err = readReports(ctx, path, ch)
			if ctx.Err() != nil {
				return
			}
			if err != nil && err.Error() != lastErr {
				lastErr = err.Error()
				if !send(ctx, ch, Event{Path: path, Err: err}) {
					return
				}
			}
			if err == nil {
				lastErr = ""
			}
			if !sleep(ctx, 2*time.Second) {
				return
			}
		}
	}()
	return ch
}

// FindHidraw returns the /dev node for a plugged-in Barracuda X receiver.
func FindHidraw() (string, error) {
	name, err := findHidrawName("/sys/class/hidraw")
	if err != nil {
		return "", err
	}
	return "/dev/" + name, nil
}

func findHidrawName(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("listing hidraw devices: %w", err)
	}
	for _, entry := range entries {
		uevent, err := os.ReadFile(filepath.Join(root, entry.Name(), "device", "uevent"))
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToUpper(string(uevent)), "HID_ID="+hidID) {
			return entry.Name(), nil
		}
	}
	return "", errors.New("Barracuda X receiver is not plugged in")
}

func readReports(ctx context.Context, path string, ch chan<- Event) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	go func() {
		<-ctx.Done()
		f.Close()
	}()
	if !send(ctx, ch, Event{Opened: true, Path: path}) {
		return ctx.Err()
	}

	buf := make([]byte, 64)
	for {
		n, err := f.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read %s: %w", path, err)
		}
		on, ok := ParseReport(buf[:n])
		if !ok {
			continue
		}
		if !send(ctx, ch, Event{On: on, Path: path}) {
			return ctx.Err()
		}
	}
}

func send(ctx context.Context, ch chan<- Event, ev Event) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// SaveState records the last earcup report, which the dongle never repeats.
func SaveState(path string, on bool) error {
	state := "off\n"
	if on {
		state = "on\n"
	}
	return os.WriteFile(path, []byte(state), 0o600)
}

// LoadState reads what SaveState wrote and when. ok is false if unknown.
func LoadState(path string) (on bool, at time.Time, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, time.Time{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, time.Time{}, false
	}
	switch strings.TrimSpace(string(data)) {
	case "on":
		return true, info.ModTime(), true
	case "off":
		return false, info.ModTime(), true
	default:
		return false, time.Time{}, false
	}
}
