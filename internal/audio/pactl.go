// Package audio lists the machine's audio outputs and moves the default one.
//
// There is a single backend and it drives pactl. PipeWire and PulseAudio look
// like two servers from the outside but like one from here, because the
// PipeWire install people actually run ships pipewire-pulse, which answers
// pactl, and because the sink list pactl returns carries the PipeWire node
// properties the old pw-dump backend went looking for, among them device.api,
// device.bus, device.form.factor and api.bluez5.address. Reading them through
// pactl costs one small JSON document per refresh instead of the entire
// PipeWire object graph, which was 486 KB and 129 objects to find four sinks.
// A machine running PulseAudio proper answers the same commands with fewer
// properties set, which the classification falls back on names to cover, so
// the only thing the server behind pactl still decides is what Name reports.
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

// Detect returns the backend for a config file's backend setting. All three
// server names stay accepted because existing configs name them, but they
// produce the same pactl backend now, and the setting only survives so that
// upgrading does not fail on a file someone already has.
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

	// The server cannot change under a running daemon, and asking costs a
	// subprocess, so the name is read here rather than on every Name call.
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

	// Losing this only costs the default marker, which is worth less than the
	// sink list itself, so the error goes no further.
	defaultName, _ := b.defaultSinkName(ctx)
	return devicesFrom(sinks, defaultName), nil
}

func (b *pactlBackend) GetDefaultSink(ctx context.Context) (*Device, error) {
	sinks, err := b.ListSinks(ctx)
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

// pactlSink is the part of a `pactl -f json list sinks` entry this package
// reads. Everything about a device that is not volume, mute or identity lives
// in properties, which is where both servers put what they know about the
// hardware behind the sink.
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

func decodeSinks(out []byte) ([]pactlSink, error) {
	var sinks []pactlSink
	if err := json.Unmarshal(out, &sinks); err != nil {
		return nil, fmt.Errorf("parsing pactl output: %w", err)
	}
	return sinks, nil
}

// devicesFrom and deviceFrom are the whole translation from what pactl says to
// what the rest of the program uses, kept apart from the subprocess so a
// captured sample can drive them in a test.
func devicesFrom(sinks []pactlSink, defaultName string) []Device {
	var devices []Device
	for _, s := range sinks {
		devices = append(devices, deviceFrom(s, defaultName))
	}
	return devices
}

func deviceFrom(s pactlSink, defaultName string) Device {
	dev := Device{
		// The sink name survives a reconnect, where the PipeWire node id the
		// old backend used was handed out fresh every time, so a priority
		// entry or a pending switch aimed at an id went stale the moment the
		// device came back.
		ID:          s.Name,
		Name:        s.Description,
		Description: s.Name,
		Type:        classify(s),
		IsDefault:   s.Name == defaultName,
		// Every sink pactl lists exists and can be selected. SUSPENDED is just
		// the resting state of a sink nobody is playing to, so treating it as
		// unavailable used to hide most of the machine.
		Available: true,
		Volume:    channelVolume(s.Volume),
		Muted:     s.Mute,
	}
	if dev.Type == DeviceTypeBluetooth {
		dev.MACAddress = macAddress(s)
	}
	return dev
}

// classify decides what kind of output a sink is. The PipeWire properties come
// first because they say so exactly, and the name matching below them is what
// is left when a plain PulseAudio server publishes none of them.
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

// isBluetooth asks every way a sink can say it came from BlueZ, because which
// one is set depends on the server and on how old the module is. node.name and
// the sink name agree on pipewire-pulse and differ on a plain server, so both
// get the prefix check.
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

// macAddress finds the address the daemon matches a BlueZ device against. The
// properties are asked first and the name is parsed last, since the name only
// carries an address at all on the PipeWire side.
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
// "3C:B0:ED:3A:2C:42". The length check is what keeps an ordinary sink name
// such as "alsa_output.pci-0000_0e_00.6.analog-stereo" from producing
// nonsense, since its second segment is not an address.
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

// channelVolume reports one level for a sink, because the channels of a sink
// move together here and the UI draws a single bar. The channel it reads has
// to be the same one every refresh or the bar flickers between near-equal
// values, and Go randomises map iteration, so the names are sorted first.
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

// parsePercent turns pactl's "40%" into 0.40. An unreadable value reports full
// volume, which is wrong in a way you can see rather than a zero bar that
// looks deliberate.
func parsePercent(s string) float64 {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if err != nil {
		return 1.0
	}
	return float64(n) / 100.0
}

// serverName reads the Server Name line of `pactl info`. pipewire-pulse
// answers "PulseAudio (on PipeWire 1.6.8)", which names both servers, so
// PipeWire wins wherever it appears. Nothing here branches on the answer, it
// is only what the status screen tells the user it is talking to.
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
