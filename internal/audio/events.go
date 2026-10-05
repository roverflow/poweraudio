package audio

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// SubscribeEvents follows `pactl subscribe` for sink and server changes.
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

// parsePactlEvent reads a line such as "Event 'change' on sink #73". The
// facility must match exactly, or sink-input events count as sink changes.
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
