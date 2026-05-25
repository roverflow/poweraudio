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

type PipeWire struct{}

func NewPipeWire() *PipeWire {
	return &PipeWire{}
}

func (p *PipeWire) Name() string {
	return "pipewire"
}

func (p *PipeWire) ListSinks(ctx context.Context) ([]Device, error) {
	defaultID, _ := p.defaultSinkID(ctx)

	out, err := exec.CommandContext(ctx, "pw-dump").Output()
	if err != nil {
		return nil, fmt.Errorf("pw-dump: %w", err)
	}

	var entries []pwDumpEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parsing pw-dump: %w", err)
	}

	var devices []Device
	for _, e := range entries {
		if e.Info == nil {
			continue
		}
		mediaClass, _ := e.Info.Props["media.class"].(string)
		if mediaClass != "Audio/Sink" {
			continue
		}

		dev := Device{
			ID:        strconv.Itoa(e.ID),
			Name:      propStr(e.Info.Props, "node.description", "node.nick", "node.name"),
			Type:      classifyDevice(e.Info.Props),
			Available: true,
			IsDefault: strconv.Itoa(e.ID) == defaultID,
		}

		// Extract BT MAC from multiple possible sources
		if mac := propStr(e.Info.Props, "api.bluez5.address"); mac != "" {
			dev.MACAddress = mac
			dev.Type = DeviceTypeBluetooth
		} else if nodeName := propStr(e.Info.Props, "node.name"); strings.HasPrefix(nodeName, "bluez_") {
			dev.Type = DeviceTypeBluetooth
			dev.MACAddress = macFromNodeName(nodeName)
		}

		if vol, ok := e.Info.Params["Props"].([]any); ok {
			dev.Volume, dev.Muted = extractVolume(vol)
		}

		devices = append(devices, dev)
	}
	return devices, nil
}

func (p *PipeWire) GetDefaultSink(ctx context.Context) (*Device, error) {
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

func (p *PipeWire) SetDefaultSink(ctx context.Context, deviceID string) error {
	out, err := exec.CommandContext(ctx, "wpctl", "set-default", deviceID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wpctl set-default %s: %s: %w", deviceID, string(out), err)
	}
	return nil
}

func (p *PipeWire) SetVolume(ctx context.Context, deviceID string, percent int) error {
	vol := fmt.Sprintf("%d%%", percent)
	out, err := exec.CommandContext(ctx, "wpctl", "set-volume", deviceID, vol).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wpctl set-volume %s %s: %s: %w", deviceID, vol, string(out), err)
	}
	return nil
}

func (p *PipeWire) ToggleMute(ctx context.Context, deviceID string) error {
	out, err := exec.CommandContext(ctx, "wpctl", "set-mute", deviceID, "toggle").CombinedOutput()
	if err != nil {
		return fmt.Errorf("wpctl set-mute %s: %s: %w", deviceID, string(out), err)
	}
	return nil
}

func (p *PipeWire) SubscribeEvents(ctx context.Context) (<-chan Event, error) {
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

func (p *PipeWire) defaultSinkID(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "wpctl", "status").Output()
	if err != nil {
		return "", err
	}
	inSinks := false
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "Sinks:") {
			inSinks = true
			continue
		}
		if inSinks {
			if trimmed == "" || (!strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "│")) {
				if !strings.Contains(trimmed, ".") && !strings.HasPrefix(trimmed, "*") {
					inSinks = false
					continue
				}
			}
			if strings.HasPrefix(trimmed, "*") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					id := strings.TrimRight(parts[1], ".")
					return id, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no default sink found in wpctl status")
}

type pwDumpEntry struct {
	ID   int         `json:"id"`
	Type string      `json:"type"`
	Info *pwDumpInfo `json:"info"`
}

type pwDumpInfo struct {
	Props  map[string]any `json:"props"`
	Params map[string]any `json:"params"`
}

func propStr(props map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := props[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func macFromNodeName(nodeName string) string {
	// Parse "bluez_output.3C_B0_ED_3A_2C_42.1" → "3C:B0:ED:3A:2C:42"
	parts := strings.SplitN(nodeName, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	mac := parts[1]
	if len(mac) != 17 { // AA_BB_CC_DD_EE_FF = 17 chars
		return ""
	}
	return strings.ReplaceAll(mac, "_", ":")
}

func classifyDevice(props map[string]any) DeviceType {
	api, _ := props["device.api"].(string)
	if api == "bluez5" {
		return DeviceTypeBluetooth
	}

	nodeName, _ := props["node.name"].(string)
	if strings.HasPrefix(nodeName, "bluez_") {
		return DeviceTypeBluetooth
	}

	bus, _ := props["device.bus"].(string)
	if bus == "usb" {
		return DeviceTypeUSB
	}

	name := strings.ToLower(propStr(props, "node.description", "node.name"))
	if strings.Contains(name, "hdmi") || strings.Contains(name, "displayport") {
		return DeviceTypeHDMI
	}
	if strings.Contains(name, "headphone") {
		return DeviceTypeHeadphone
	}

	return DeviceTypeSpeaker
}

func extractVolume(propsArr []any) (float64, bool) {
	for _, item := range propsArr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if vols, ok := m["channelVolumes"].([]any); ok && len(vols) > 0 {
			if v, ok := vols[0].(float64); ok {
				muted, _ := m["mute"].(bool)
				return v, muted
			}
		}
	}
	return 1.0, false
}

func parsePactlEvent(line string) (Event, bool) {
	lower := strings.ToLower(line)

	var ev Event
	switch {
	case strings.Contains(lower, "'new'") && strings.Contains(lower, "sink"):
		ev.Type = EventSinkAdded
	case strings.Contains(lower, "'remove'") && strings.Contains(lower, "sink"):
		ev.Type = EventSinkRemoved
	case strings.Contains(lower, "'change'") && strings.Contains(lower, "server"):
		ev.Type = EventDefaultChanged
	case strings.Contains(lower, "'change'") && strings.Contains(lower, "sink"):
		ev.Type = EventSinkChanged
	default:
		return Event{}, false
	}

	if idx := strings.Index(lower, "#"); idx >= 0 {
		rest := line[idx+1:]
		parts := strings.Fields(rest)
		if len(parts) > 0 {
			ev.DeviceID = parts[0]
		}
	}

	return ev, true
}
