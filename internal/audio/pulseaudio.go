package audio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type PulseAudio struct{}

func NewPulseAudio() *PulseAudio {
	return &PulseAudio{}
}

func (p *PulseAudio) Name() string {
	return "pulseaudio"
}

func (p *PulseAudio) ListSinks(ctx context.Context) ([]Device, error) {
	out, err := exec.CommandContext(ctx, "pactl", "-f", "json", "list", "sinks").Output()
	if err != nil {
		return nil, fmt.Errorf("pactl list sinks: %w", err)
	}

	var sinks []pactlSink
	if err := json.Unmarshal(out, &sinks); err != nil {
		return nil, fmt.Errorf("parsing pactl output: %w", err)
	}

	defaultName, _ := p.defaultSinkName(ctx)

	var devices []Device
	for _, s := range sinks {
		dev := Device{
			ID:          s.Name,
			Name:        s.Description,
			Description: s.Description,
			IsDefault:   s.Name == defaultName,
			// Every sink pactl lists exists and can be selected. SUSPENDED is
			// just the resting state of a sink nobody is playing to, so
			// treating it as unavailable used to hide most of the machine.
			Available: true,
			Muted:     s.Mute,
		}

		for _, ch := range s.Volume {
			dev.Volume = parsePercent(ch.ValuePercent)
			break
		}

		dev.Type = classifyPulseDevice(s)
		if mac, ok := s.Properties["bluetooth.device.mac"]; ok {
			dev.MACAddress = mac
			dev.Type = DeviceTypeBluetooth
		}

		devices = append(devices, dev)
	}
	return devices, nil
}

func (p *PulseAudio) GetDefaultSink(ctx context.Context) (*Device, error) {
	sinks, err := p.ListSinks(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range sinks {
		if s.IsDefault {
			return &s, nil
		}
	}
	if len(sinks) > 0 {
		return &sinks[0], nil
	}
	return nil, fmt.Errorf("no audio sinks found")
}

func (p *PulseAudio) SetDefaultSink(ctx context.Context, deviceID string) error {
	out, err := exec.CommandContext(ctx, "pactl", "set-default-sink", deviceID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pactl set-default-sink: %s: %w", string(out), err)
	}
	return nil
}

func (p *PulseAudio) SetVolume(ctx context.Context, deviceID string, percent int) error {
	vol := fmt.Sprintf("%d%%", percent)
	out, err := exec.CommandContext(ctx, "pactl", "set-sink-volume", deviceID, vol).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pactl set-sink-volume %s %s: %s: %w", deviceID, vol, string(out), err)
	}
	return nil
}

func (p *PulseAudio) ToggleMute(ctx context.Context, deviceID string) error {
	out, err := exec.CommandContext(ctx, "pactl", "set-sink-mute", deviceID, "toggle").CombinedOutput()
	if err != nil {
		return fmt.Errorf("pactl set-sink-mute %s: %s: %w", deviceID, string(out), err)
	}
	return nil
}

func (p *PulseAudio) SubscribeEvents(ctx context.Context) (<-chan Event, error) {
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

func (p *PulseAudio) defaultSinkName(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "pactl", "get-default-sink").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type pactlSink struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	State       string                     `json:"state"`
	Mute        bool                       `json:"mute"`
	Volume      map[string]pactlSinkVolume `json:"volume"`
	Properties  map[string]string          `json:"properties"`
}

type pactlSinkVolume struct {
	Value int `json:"value"`
	// pactl renders this as "40%", not as a number. Decoding it into an int
	// fails the whole document, which took the entire backend down with it.
	ValuePercent string `json:"value_percent"`
	DB           string `json:"db"`
}

// parsePercent turns pactl's "40%" into 0.40. An unreadable value reports
// full volume, which is wrong in a way you can see rather than a zero bar
// that looks deliberate.
func parsePercent(s string) float64 {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if err != nil {
		return 1.0
	}
	return float64(n) / 100.0
}

func classifyPulseDevice(s pactlSink) DeviceType {
	name := strings.ToLower(s.Description + " " + s.Name)

	if strings.Contains(name, "bluez") || strings.Contains(name, "bluetooth") {
		return DeviceTypeBluetooth
	}
	if strings.Contains(name, "hdmi") || strings.Contains(name, "displayport") {
		return DeviceTypeHDMI
	}
	if strings.Contains(name, "usb") {
		return DeviceTypeUSB
	}
	if strings.Contains(name, "headphone") {
		return DeviceTypeHeadphone
	}
	return DeviceTypeSpeaker
}
