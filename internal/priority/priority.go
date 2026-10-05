// Package priority ranks devices against the user's priority list. The
// daemon and the UI share it so they never disagree about a match.
package priority

import (
	"strings"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
)

// Matches reports whether entry's match string is a case-insensitive
// substring of dev's name, description or MAC, and any set type agrees.
func Matches(dev audio.Device, entry config.PriorityEntry) bool {
	match := strings.ToLower(strings.TrimSpace(entry.Match))
	if match == "" {
		// strings.Contains matches "", so an empty key matches every device.
		return false
	}
	if entry.Type != "" && !strings.EqualFold(entry.Type, dev.Type.String()) {
		return false
	}
	name := strings.ToLower(dev.Name)
	desc := strings.ToLower(dev.Description)
	mac := strings.ToLower(dev.MACAddress)

	return strings.Contains(name, match) ||
		strings.Contains(desc, match) ||
		(mac != "" && strings.Contains(mac, match))
}

// Rank is the index of the first entry matching dev, or len(entries).
func Rank(dev audio.Device, entries []config.PriorityEntry) int {
	for i, entry := range entries {
		if Matches(dev, entry) {
			return i
		}
	}
	return len(entries)
}

// Best returns the first usable device in ranking order, else the first
// usable non-virtual device, else nil. Virtual sinks need an entry.
func Best(devices []audio.Device, entries []config.PriorityEntry) *audio.Device {
	for _, entry := range entries {
		for i := range devices {
			if devices[i].Usable() && Matches(devices[i], entry) {
				return &devices[i]
			}
		}
	}
	for i := range devices {
		if devices[i].Usable() && !devices[i].Virtual {
			return &devices[i]
		}
	}
	return nil
}

// Present reports whether a device that can play matches entry.
func Present(entry config.PriorityEntry, devices []audio.Device) bool {
	for _, dev := range devices {
		if dev.Usable() && Matches(dev, entry) {
			return true
		}
	}
	return false
}

// Unranked returns the devices no entry matches, in their original order.
func Unranked(devices []audio.Device, entries []config.PriorityEntry) []audio.Device {
	var out []audio.Device
	for _, dev := range devices {
		if Rank(dev, entries) == len(entries) {
			out = append(out, dev)
		}
	}
	return out
}
