// Package priority ranks audio devices against the user's ordered list of
// priority entries. The daemon uses it to pick a fallback and to decide
// whether a connecting device outranks the current one; the UI uses it to
// show which entries are present and which devices are still unranked. Both
// go through the same functions so the two never disagree about a match.
package priority

import (
	"strings"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
)

// Matches reports whether entry describes dev. The match string is a
// case-insensitive substring of the device's name, description or MAC
// address. An entry that also sets a type requires the device's detected type
// to equal it.
func Matches(dev audio.Device, entry config.PriorityEntry) bool {
	match := strings.ToLower(strings.TrimSpace(entry.Match))
	if match == "" {
		// strings.Contains is true for the empty string, so an entry with no
		// match key used to claim every device and quietly outrank the rest
		// of the list.
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

// Rank is the position of the first entry that matches dev, so lower is
// better. A device no entry matches ranks len(entries), below everything on
// the list.
func Rank(dev audio.Device, entries []config.PriorityEntry) int {
	for i, entry := range entries {
		if Matches(dev, entry) {
			return i
		}
	}
	return len(entries)
}

// Best walks the ranking top down and returns the first available device an
// entry matches. When nothing on the list is present it returns the first
// available device, and nil when there are none.
func Best(devices []audio.Device, entries []config.PriorityEntry) *audio.Device {
	for _, entry := range entries {
		for i := range devices {
			if devices[i].Available && Matches(devices[i], entry) {
				return &devices[i]
			}
		}
	}
	for i := range devices {
		if devices[i].Available {
			return &devices[i]
		}
	}
	return nil
}

// Present reports whether any of devices matches entry.
func Present(entry config.PriorityEntry, devices []audio.Device) bool {
	for _, dev := range devices {
		if Matches(dev, entry) {
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
