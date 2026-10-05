package cli

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
	"github.com/roverflow/poweraudio/internal/priority"
)

// Above 100 is PipeWire software gain, which most hardware distorts.
const (
	minVolume = 0
	maxVolume = 150
)

type matchKind int

const (
	matchNone matchKind = iota
	matchPartial
	matchExact
)

// findDevice resolves a query to one device. An exact match on any field
// wins over substrings, and an ambiguous query is an error, not a guess.
func findDevice(devices []audio.Device, query string) (*audio.Device, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, usagef("empty device query")
	}
	if len(devices) == 0 {
		return nil, errors.New("the daemon reports no output devices")
	}

	var exact, partial []int
	for i := range devices {
		switch classify(devices[i], q) {
		case matchExact:
			exact = append(exact, i)
		case matchPartial:
			partial = append(partial, i)
		}
	}

	switch {
	case len(exact) == 1:
		return &devices[exact[0]], nil
	case len(exact) > 1:
		return nil, ambiguousError(devices, exact, query)
	case len(partial) == 1:
		return &devices[partial[0]], nil
	case len(partial) > 1:
		return nil, ambiguousError(devices, partial, query)
	}

	return nil, fmt.Errorf("no device matches %q (devices: %s)", query, deviceNames(devices, indexes(devices)))
}

func classify(dev audio.Device, q string) matchKind {
	kind := matchNone
	for _, field := range []string{dev.ID, dev.Name, dev.Description, dev.MACAddress} {
		if field == "" {
			continue
		}
		f := strings.ToLower(field)
		if f == q {
			return matchExact
		}
		if strings.Contains(f, q) {
			kind = matchPartial
		}
	}
	return kind
}

func ambiguousError(devices []audio.Device, candidates []int, query string) error {
	return fmt.Errorf("%q matches more than one device: %s", query, deviceNames(devices, candidates))
}

func deviceNames(devices []audio.Device, candidates []int) string {
	names := make([]string, 0, len(candidates))
	for _, i := range candidates {
		names = append(names, devices[i].Name)
	}
	return strings.Join(names, ", ")
}

func indexes(devices []audio.Device) []int {
	all := make([]int, len(devices))
	for i := range devices {
		all[i] = i
	}
	return all
}

// nextDevice is the next playable device after the default, wrapping
// around. It skips virtual sinks the ranking does not name, such as
// EasyEffects chains.
func nextDevice(devices []audio.Device, ranking []config.PriorityEntry) (*audio.Device, error) {
	n := len(devices)
	if n == 0 {
		return nil, errors.New("the daemon reports no output devices")
	}

	start := -1
	for i := range devices {
		if devices[i].IsDefault {
			start = i
			break
		}
	}

	for offset := 1; offset <= n; offset++ {
		candidate := &devices[(start+offset+n)%n]
		if !candidate.Usable() {
			continue
		}
		if candidate.Virtual && priority.Rank(*candidate, ranking) >= len(ranking) {
			continue
		}
		return candidate, nil
	}
	return nil, errors.New("no other output can play right now")
}

func targetDevice(snap *ipc.Snapshot, query string) (*audio.Device, error) {
	if query != "" {
		return findDevice(snap.Devices, query)
	}
	if def := snap.Default(); def != nil {
		return def, nil
	}
	return nil, errors.New("there is no default device, name one with --device")
}

func deviceByID(devices []audio.Device, id string) *audio.Device {
	for i := range devices {
		if devices[i].ID == id {
			return &devices[i]
		}
	}
	return nil
}

// volumeSpec is a parsed volume argument. A leading sign makes it relative.
type volumeSpec struct {
	value    int
	relative bool
}

func parseVolumeSpec(arg string) (volumeSpec, error) {
	s := strings.TrimSuffix(strings.TrimSpace(arg), "%")
	if s == "" {
		return volumeSpec{}, usagef("volume needs a level such as 50, +10 or -10")
	}

	relative := s[0] == '+' || s[0] == '-'
	n, err := strconv.Atoi(s)
	if err != nil {
		return volumeSpec{}, usagef("invalid volume %q, want a level such as 50, +10 or -10", arg)
	}
	return volumeSpec{value: n, relative: relative}, nil
}

func (v volumeSpec) apply(current int) int {
	target := v.value
	if v.relative {
		target = current + v.value
	}
	return clampVolume(target)
}

func clampVolume(percent int) int {
	if percent < minVolume {
		return minVolume
	}
	if percent > maxVolume {
		return maxVolume
	}
	return percent
}

func volumePercent(volume float64) int {
	return int(math.Round(volume * 100))
}
