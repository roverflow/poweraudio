package cli

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/ipc"
)

// The range the backends accept. Above 100 is software gain, which PipeWire
// allows and most hardware turns into distortion, so 150 is where it stops.
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

// findDevice resolves a user's query to exactly one device. An exact match on
// any field wins outright, so a device whose name is a substring of another
// one can still be named. Failing that the query has to be a substring of one
// device and one only, because switching the output to whichever candidate
// happened to be listed first is worse than refusing.
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

// classify reports how well q describes dev. Every field is checked before
// settling for a partial match, so an exact hit on the MAC address is not
// lost to a substring of the name.
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

// nextDevice is the device after the current default in list order, wrapping
// past the end and skipping anything the backend reports as unavailable. With
// no default it starts from the top of the list, which is what a first press
// of the media key should do.
func nextDevice(devices []audio.Device) (*audio.Device, error) {
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
		if candidate.Available {
			return candidate, nil
		}
	}
	return nil, errors.New("no available device to switch to")
}

// targetDevice is the device a command acts on: the one the query names, or
// the current default when there is no query.
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

// volumeSpec is a parsed volume argument. A leading sign makes it relative to
// whatever the device is at now, anything else is an absolute percentage.
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

// apply resolves the spec against the device's current level and clamps it,
// so "volume -20" on a device at 10 percent lands on zero instead of asking
// the backend for something it would reject.
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

// volumePercent converts the backend's 0.0 to 1.5 scale to whole percent.
func volumePercent(volume float64) int {
	return int(math.Round(volume * 100))
}
