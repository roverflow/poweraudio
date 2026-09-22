// Package razer reads the Barracuda X dongle's earcup report.
//
// The USB receiver stays a sound card while the earcups are powered off, and
// PipeWire keeps the port at "availability unknown" either way. The dongle
// pushes one vendor HID report when that changes and stays quiet in between,
// so a daemon that starts after the headset is already off hears nothing
// until the next press.
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

	// hidID is the HID_ID line in the dongle's hidraw uevent.
	hidID = "0003:00001532:0000054E"

	reportID = 0x02

	// connectedByte is 0x01 while the earcups are on and 0x00 when they are
	// off. Captured from this dongle on 2026-09-22: the rest of the 64-byte
	// report is a counter plus a payload that did not change across a power
	// cycle. Index 13 is the only byte that followed the switch.
	connectedByte = 13
)

// IsBarracuda reports whether dev is the Barracuda X receiver. The USB ids
// come from pactl. The sink name is the fallback for a server that omits them.
func IsBarracuda(dev audio.Device) bool {
	if dev.VendorID == VendorID && dev.ProductID == ProductID {
		return true
	}
	return strings.Contains(dev.ID, "usb-1532_Razer_Barracuda_X")
}

// ParseReport reads one hidraw buffer. ok is false for a short read, a
// different report, or a value other than the on and off bytes, so a report
// this dongle has not been seen to send cannot move the output.
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

// Event is one thing the watcher learned. Err is an open or read failure.
// Opened means the hidraw node was opened and reports will follow. On is only
// meaningful when Err is nil and Opened is false.
type Event struct {
	On     bool
	Opened bool
	Path   string
	Err    error
}

// Watch reads the dongle until ctx ends. The node name can change across a
// replug, so a failed read starts the search again. A missing dongle is
// quiet. The first open error is reported once, until a later open works.
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
