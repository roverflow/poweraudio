package audio

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// SubscribeEvents follows `pactl subscribe`, which reports sinks appearing and
// going away as they happen. The daemon's timers exist for the case where this
// says nothing, so a slow adapter still lands eventually, but on a normal
// machine the switch happens on the line this reads.
func (b *pactlBackend) SubscribeEvents(ctx context.Context) (<-chan Event, error) {
	cmd := exec.CommandContext(ctx, "pactl", "subscribe")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("pactl subscribe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting pactl subscribe: %w", err)
	}

	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		defer cmd.Wait()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if ev, ok := parsePactlEvent(line); ok {
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch, nil
}

// parsePactlEvent reads a line of `pactl subscribe` output, which looks like
//
//	Event 'change' on sink #73
//
// Only sinks and the server matter here. Matching the facility by substring
// meant every sink-input event counted as a change to the outputs, and an
// application starting or stopping playback emits those constantly.
func parsePactlEvent(line string) (Event, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[0] != "Event" {
		return Event{}, false
	}
	action := strings.Trim(fields[1], "'")
	facility := fields[3]

	var ev Event
	switch {
	case facility == "server" && action == "change":
		ev.Type = EventDefaultChanged
	case facility != "sink":
		return Event{}, false
	case action == "new":
		ev.Type = EventSinkAdded
	case action == "remove":
		ev.Type = EventSinkRemoved
	case action == "change":
		ev.Type = EventSinkChanged
	default:
		return Event{}, false
	}

	if len(fields) > 4 {
		ev.DeviceID = strings.TrimPrefix(fields[4], "#")
	}
	return ev, true
}
