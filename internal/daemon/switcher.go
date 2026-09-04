package daemon

import (
	"strings"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
)

func FindBestDevice(available []audio.Device, priorities []config.PriorityEntry) *audio.Device {
	for _, prio := range priorities {
		for i := range available {
			if !available[i].Available {
				continue
			}
			if matchesPriority(available[i], prio) {
				return &available[i]
			}
		}
	}
	for i := range available {
		if available[i].Available {
			return &available[i]
		}
	}
	return nil
}

func DevicePriority(dev audio.Device, priorities []config.PriorityEntry) int {
	for i, prio := range priorities {
		if matchesPriority(dev, prio) {
			return i
		}
	}
	return len(priorities)
}

func matchesPriority(dev audio.Device, prio config.PriorityEntry) bool {
	match := strings.ToLower(strings.TrimSpace(prio.Match))
	if match == "" {
		// strings.Contains is true for the empty string, so an entry with no
		// match key used to claim every device and quietly outrank the rest
		// of the list.
		return false
	}
	if prio.Type != "" && !strings.EqualFold(prio.Type, dev.Type.String()) {
		return false
	}
	name := strings.ToLower(dev.Name)
	desc := strings.ToLower(dev.Description)
	mac := strings.ToLower(dev.MACAddress)

	return strings.Contains(name, match) ||
		strings.Contains(desc, match) ||
		(mac != "" && strings.Contains(mac, match))
}
