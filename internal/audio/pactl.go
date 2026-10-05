// Package audio lists the machine's audio outputs and moves the default one
// through pactl, which PulseAudio and PipeWire's pipewire-pulse both answer.
package audio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// Detect returns the pactl backend. It still accepts every old backend
// setting so existing configs load.
func Detect(preference string) (Backend, error) {
	switch preference {
	case "", "auto", "pipewire", "pulseaudio":
	default:
		return nil, fmt.Errorf("unknown backend %q, expected auto, pipewire or pulseaudio", preference)
	}

	if _, err := exec.LookPath("pactl"); err != nil {
		return nil, errors.New("pactl is not on PATH, install PipeWire with pipewire-pulse, or PulseAudio")
	}

	out, err := exec.Command("pactl", "info").Output()
	if err != nil {
		return nil, fmt.Errorf("pactl info failed, so no sound server is answering: start PipeWire with pipewire-pulse, or PulseAudio: %w", err)
	}

	return &pactlBackend{name: serverName(string(out))}, nil
}

type pactlBackend struct {
	name string
}

func (b *pactlBackend) Name() string {
	return b.name
}

func (b *pactlBackend) ListSinks(ctx context.Context) ([]Device, error) {
	out, err := exec.CommandContext(ctx, "pactl", "-f", "json", "list", "sinks").Output()
	if err != nil {
		return nil, fmt.Errorf("pactl list sinks: %w", err)
	}

	sinks, err := decodeSinks(out)
	if err != nil {
		return nil, err
	}

	// A failed lookup only loses the default marker, not the sink list.
	defaultName, _ := b.defaultSinkName(ctx)
	return devicesFrom(sinks, defaultName), nil
}

func (b *pactlBackend) DefaultSinkName(ctx context.Context) (string, error) {
	return b.defaultSinkName(ctx)
}

func (b *pactlBackend) SetDefaultSink(ctx context.Context, deviceID string) error {
	out, err := exec.CommandContext(ctx, "pactl", "set-default-sink", deviceID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pactl set-default-sink %s: %s: %w", deviceID, string(out), err)
	}
	return nil
}

func (b *pactlBackend) SetVolume(ctx context.Context, deviceID string, percent int) error {
	vol := fmt.Sprintf("%d%%", percent)
	out, err := exec.CommandContext(ctx, "pactl", "set-sink-volume", deviceID, vol).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pactl set-sink-volume %s %s: %s: %w", deviceID, vol, string(out), err)
	}
	return nil
}

func (b *pactlBackend) ToggleMute(ctx context.Context, deviceID string) error {
	out, err := exec.CommandContext(ctx, "pactl", "set-sink-mute", deviceID, "toggle").CombinedOutput()
	if err != nil {
		return fmt.Errorf("pactl set-sink-mute %s: %s: %w", deviceID, string(out), err)
	}
	return nil
}

func (b *pactlBackend) defaultSinkName(ctx context.Context) (string, error) {
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
	// Both are empty when the server omits them.
	ActivePort string      `json:"active_port"`
	Ports      []pactlPort `json:"ports"`
}

type pactlPort struct {
	Name         string `json:"name"`
	Availability string `json:"availability"`
}

type pactlSinkVolume struct {
	Value int `json:"value"`
	// pactl prints this as "40%", not a number.
	ValuePercent string `json:"value_percent"`
	DB           string `json:"db"`
}

func decodeSinks(out []byte) ([]pactlSink, error) {
	var sinks []pactlSink
	if err := json.Unmarshal(out, &sinks); err != nil {
		return nil, fmt.Errorf("parsing pactl output: %w", err)
	}
	return sinks, nil
}

func devicesFrom(sinks []pactlSink, defaultName string) []Device {
	var devices []Device
	for _, s := range sinks {
		devices = append(devices, deviceFrom(s, defaultName))
	}
	return devices
}

func deviceFrom(s pactlSink, defaultName string) Device {
	dev := Device{
		// Sink names survive a reconnect. PipeWire node ids do not.
		ID:          s.Name,
		Name:        displayName(s),
		Description: s.Name,
		Type:        classify(s),
		IsDefault:   s.Name == defaultName,
		Available:   sinkAvailable(s),
		Volume:      channelVolume(s.Volume),
		Muted:       s.Mute,
		Virtual:     isVirtual(s),
	}
	if dev.Type == DeviceTypeBluetooth {
		dev.MACAddress = macAddress(s)
	}
	dev.VendorID = parseHexID(s.Properties["device.vendor.id"])
	dev.ProductID = parseHexID(s.Properties["device.product.id"])
	return dev
}

// pactl prints "(null)" for a sink with no description, such as an AirPlay
// speaker that announces no name.
func displayName(s pactlSink) string {
	desc := strings.TrimSpace(s.Description)
	if desc == "" || desc == "(null)" {
		return s.Name
	}
	return desc
}

// Name prefixes of network sinks, for servers that do not set node.network.
var networkSinkPrefixes = []string{"raop_sink.", "raop_output.", "tunnel.", "tunnel-sink."}

// isVirtual reports a sink with no local hardware behind it. Network sinks
// count, so the fallback never sends audio to a speaker in another room.
func isVirtual(s pactlSink) bool {
	if s.Name == PlaceholderID {
		return true
	}
	if s.Properties["node.virtual"] == "true" || s.Properties["node.network"] == "true" {
		return true
	}
	for _, prefix := range networkSinkPrefixes {
		if strings.HasPrefix(s.Name, prefix) {
			return true
		}
	}
	if s.Properties["factory.name"] == "support.null-audio-sink" {
		return true
	}
	switch s.Properties["device.class"] {
	case "abstract", "filter", "monitor":
		return true
	}
	return false
}

func parseHexID(s string) uint16 {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"))
	if s == "" {
		return 0
	}
	n, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0
	}
	return uint16(n)
}

// sinkAvailable reports whether audio can come out of this sink. Unknown
// counts as available because the Barracuda dongle has no jack sense and
// reports "availability unknown" the whole time it is plugged in.
func sinkAvailable(s pactlSink) bool {
	if s.ActivePort != "" {
		for _, p := range s.Ports {
			if p.Name == s.ActivePort {
				return portAvailable(p.Availability)
			}
		}
	}
	if len(s.Ports) == 0 {
		return true
	}
	for _, p := range s.Ports {
		if portAvailable(p.Availability) {
			return true
		}
	}
	return false
}

func portAvailable(availability string) bool {
	switch strings.ToLower(strings.TrimSpace(availability)) {
	case "no", "not available", "unavailable":
		return false
	default:
		return true
	}
}

// PipeWire properties come first. Name matching covers plain PulseAudio.
func classify(s pactlSink) DeviceType {
	if isBluetooth(s) {
		return DeviceTypeBluetooth
	}

	switch s.Properties["device.form.factor"] {
	case "headphone", "headset":
		return DeviceTypeHeadphone
	}
	if s.Properties["device.bus"] == "usb" {
		return DeviceTypeUSB
	}

	name := strings.ToLower(s.Description + " " + s.Name)
	if strings.Contains(name, "hdmi") || strings.Contains(name, "displayport") {
		return DeviceTypeHDMI
	}
	if strings.Contains(name, "bluez") || strings.Contains(name, "bluetooth") {
		return DeviceTypeBluetooth
	}
	if strings.Contains(name, "usb") {
		return DeviceTypeUSB
	}
	if strings.Contains(name, "headphone") {
		return DeviceTypeHeadphone
	}
	return DeviceTypeSpeaker
}

// node.name and the sink name differ on plain PulseAudio, so check both.
func isBluetooth(s pactlSink) bool {
	if s.Properties["device.api"] == "bluez5" {
		return true
	}
	if s.Properties["api.bluez5.address"] != "" || s.Properties["bluetooth.device.mac"] != "" {
		return true
	}
	return strings.HasPrefix(s.Properties["node.name"], "bluez_") ||
		strings.HasPrefix(s.Name, "bluez_")
}

func macAddress(s pactlSink) string {
	if mac := s.Properties["api.bluez5.address"]; mac != "" {
		return mac
	}
	if mac := s.Properties["bluetooth.device.mac"]; mac != "" {
		return mac
	}
	if mac := macFromNodeName(s.Properties["node.name"]); mac != "" {
		return mac
	}
	return macFromNodeName(s.Name)
}

// macFromNodeName turns "bluez_output.3C_B0_ED_3A_2C_42.1" into
// "3C:B0:ED:3A:2C:42".
func macFromNodeName(nodeName string) string {
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

// Map order is random, so sort the channel names or the volume bar flickers.
func channelVolume(vol map[string]pactlSinkVolume) float64 {
	names := make([]string, 0, len(vol))
	for name := range vol {
		names = append(names, name)
	}
	if len(names) == 0 {
		return 1.0
	}
	slices.Sort(names)
	return parsePercent(vol[names[0]].ValuePercent)
}

// An unreadable value reads as full volume, not a deliberate-looking zero.
func parsePercent(s string) float64 {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if err != nil {
		return 1.0
	}
	return float64(n) / 100.0
}

// pipewire-pulse answers "PulseAudio (on PipeWire 1.6.8)", so PipeWire wins.
func serverName(info string) string {
	for _, line := range strings.Split(info, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Server Name:")
		if !ok {
			continue
		}
		if strings.Contains(rest, "PipeWire") {
			return "pipewire"
		}
		return "pulseaudio"
	}
	return "pulseaudio"
}
