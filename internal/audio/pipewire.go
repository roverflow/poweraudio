package audio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	// One wpctl run answers both questions the sink list needs from
	// WirePlumber: which node is default, and what each node's volume is.
	// Asking per device cost a subprocess per sink on every refresh.
	defaultID, levels := p.wpctlSinks(ctx)

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

		if lvl, ok := levels[dev.ID]; ok {
			dev.Volume, dev.Muted = lvl.volume, lvl.muted
		} else if vol, muted, err := wpctlGetVolume(ctx, dev.ID); err == nil {
			// wpctl status did not list this node, which happens while a sink
			// is still appearing. Ask about it directly before falling back to
			// pw-dump, whose scale does not match what set-volume expects.
			dev.Volume, dev.Muted = vol, muted
		} else if propVol, ok := e.Info.Params["Props"].([]any); ok {
			dev.Volume, dev.Muted = extractVolume(propVol)
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

// sinkLevel is what a single line of the Sinks section says about a node.
type sinkLevel struct {
	volume float64
	muted  bool
}

// wpctlSinks reads the Sinks section of `wpctl status`. Each row looks like
//
//	│  *   67. Ryzen HD Audio Controller Analog Stereo [vol: 0.45]
//
// with a box-drawing tree down the left margin, an optional * on the default,
// the node id, the description, and the volume in the same scale that
// `wpctl set-volume` accepts. It returns the default node id and a level per
// node id. A failure here is not fatal: callers fall back per device.
func (p *PipeWire) wpctlSinks(ctx context.Context) (string, map[string]sinkLevel) {
	out, err := exec.CommandContext(ctx, "wpctl", "status").Output()
	if err != nil {
		return "", map[string]sinkLevel{}
	}
	return parseWpctlStatus(string(out))
}

// parseWpctlStatus is wpctlSinks without the subprocess, so the shape of the
// output can be tested against a captured sample.
func parseWpctlStatus(out string) (string, map[string]sinkLevel) {
	levels := make(map[string]sinkLevel)

	var defaultID string
	inSinks := false
	for _, line := range strings.Split(out, "\n") {
		// The tree is never part of the content, and the default marker is
		// never the first character of the raw line. Strip the tree first.
		clean := strings.TrimSpace(strings.TrimLeft(line, " \t\u2502\u251c\u2514\u2500\u250c\u2510\u2524\u252c\u2534\u253c"))

		if strings.HasSuffix(clean, ":") {
			inSinks = clean == "Sinks:"
			continue
		}
		if !inSinks {
			continue
		}

		isDefault := strings.HasPrefix(clean, "*")
		fields := strings.Fields(strings.TrimPrefix(clean, "*"))
		if len(fields) == 0 {
			continue
		}
		id := strings.TrimSuffix(fields[0], ".")
		if id == "" || strings.Trim(id, "0123456789") != "" {
			continue
		}

		if isDefault && defaultID == "" {
			defaultID = id
		}
		if vol, muted, ok := parseVolumeTag(clean); ok {
			levels[id] = sinkLevel{volume: vol, muted: muted}
		}
	}
	return defaultID, levels
}

// parseVolumeTag pulls the level out of the "[vol: 0.45]" suffix wpctl appends
// to each node, which reads "[vol: 0.45 MUTED]" when the node is muted.
func parseVolumeTag(line string) (float64, bool, bool) {
	i := strings.Index(line, "[vol:")
	if i < 0 {
		return 0, false, false
	}
	rest := line[i+len("[vol:"):]
	j := strings.Index(rest, "]")
	if j < 0 {
		return 0, false, false
	}
	tag := rest[:j]

	fields := strings.Fields(tag)
	if len(fields) == 0 {
		return 0, false, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false, false
	}
	return v, strings.Contains(tag, "MUTED"), true
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

func wpctlGetVolume(ctx context.Context, deviceID string) (float64, bool, error) {
	out, err := exec.CommandContext(ctx, "wpctl", "get-volume", deviceID).CombinedOutput()
	if err != nil {
		return 0, false, err
	}
	s := strings.TrimSpace(string(out))
	// Format: "Volume: 0.95" or "Volume: 0.95 [MUTED]"
	muted := strings.Contains(s, "[MUTED]")
	s = strings.TrimPrefix(s, "Volume: ")
	s = strings.TrimSuffix(s, " [MUTED]")
	s = strings.TrimSpace(s)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parsing wpctl volume %q: %w", string(out), err)
	}
	return v, muted, nil
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
				return math.Cbrt(v), muted
			}
		}
	}
	return 1.0, false
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
