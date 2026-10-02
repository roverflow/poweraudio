// Package probe works out which audio stack the daemon is running on, so the
// rest of poweraudio can pick the switching engine and the settings that this
// machine actually supports.
//
// It asks what the running programs can do rather than trusting a version
// number alone. A package upgrade leaves the old WirePlumber running until the
// next restart, and several of its settings arrived partway through 0.5, so
// the live settings list is read from WirePlumber itself.
//
// Everything here is three short subprocess calls, about 15ms on a desktop.
// pw-dump would answer the same questions but prints the whole PipeWire graph,
// roughly half a megabyte, so it is not used.
package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Server names the sound server that answers pactl.
const (
	ServerPipeWire   = "PipeWire"
	ServerPulseAudio = "PulseAudio"
)

// Session manager names, as their clients report them.
const (
	ManagerWirePlumber  = "WirePlumber"
	ManagerMediaSession = "pipewire-media-session"
)

// Tier is how much of poweraudio a machine can use. It is derived from a
// Report rather than stored, so a report decoded from an older daemon still
// answers.
type Tier string

const (
	// TierUnknown means no sound server answered.
	TierUnknown Tier = "unknown"
	// TierPulseAudio is plain PulseAudio, driven through pactl.
	TierPulseAudio Tier = "pulseaudio"
	// TierPipeWire is PipeWire with no session manager poweraudio knows,
	// which in practice means pipewire-media-session.
	TierPipeWire Tier = "pipewire"
	// TierWirePlumber04 is WirePlumber 0.4: Lua config, no hooks for the
	// default device.
	TierWirePlumber04 Tier = "wireplumber-0.4"
	// TierWirePlumber05 is WirePlumber 0.5 or newer: SPA-JSON config, live
	// settings, and event hooks.
	TierWirePlumber05 Tier = "wireplumber-0.5"
)

// Report is what one probe found. Empty strings mean the probe could not tell.
type Report struct {
	Server         string `json:"server,omitempty"`
	ServerVersion  string `json:"server_version,omitempty"`
	Manager        string `json:"manager,omitempty"`
	ManagerVersion string `json:"manager_version,omitempty"`

	// Settings are WirePlumber's live settings and their values, read from
	// its sm-settings metadata. Only 0.5 publishes them, and the keys present
	// say which settings this version has.
	Settings map[string]string `json:"settings,omitempty"`

	// PactlJSON is whether pactl can print JSON, which the backend needs.
	// pactl gained --format in PulseAudio 16, and Ubuntu 22.04's 15.99.1
	// already has it.
	PactlJSON bool `json:"pactl_json"`

	// Problems are things the probe tried and could not do, worded for the
	// status screen.
	Problems []string `json:"problems,omitempty"`

	ProbedAt time.Time `json:"probed_at"`
}

// Tier places the report in one of the tiers above.
func (r Report) Tier() Tier {
	switch {
	case r.Server == "":
		return TierUnknown
	case r.Server == ServerPulseAudio:
		return TierPulseAudio
	case r.Manager == ManagerWirePlumber:
		if wirePlumber05(r) {
			return TierWirePlumber05
		}
		return TierWirePlumber04
	default:
		return TierPipeWire
	}
}

// wirePlumber05 decides the WirePlumber generation. The version decides when
// it is there; the settings metadata, which only 0.5 creates, decides when it
// is not.
func wirePlumber05(r Report) bool {
	if major, minor, ok := majorMinor(r.ManagerVersion); ok {
		return major > 0 || minor >= 5
	}
	return len(r.Settings) > 0
}

// HasSetting reports whether this WirePlumber knows a live setting, so a
// toggle is only offered where flipping it does something.
func (r Report) HasSetting(name string) bool {
	_, ok := r.Settings[name]
	return ok
}

// Summary is one line for a person: "PipeWire 1.6.9, WirePlumber 0.5.17".
func (r Report) Summary() string {
	if r.Server == "" {
		return "no sound server answered"
	}
	parts := []string{join(r.Server, r.ServerVersion)}
	switch {
	case r.Manager != "":
		parts = append(parts, join(r.Manager, r.ManagerVersion))
	case r.Server == ServerPipeWire:
		parts = append(parts, "no session manager found")
	}
	return strings.Join(parts, ", ")
}

func join(name, version string) string {
	if version == "" {
		return name
	}
	return name + " " + version
}

// Runner runs one command and returns its standard output. Tests replace it
// with captured output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// commandTimeout bounds each call. At login pipewire-pulse can take a moment
// to answer, but a probe that hangs would hold up whatever asked for it.
const commandTimeout = 3 * time.Second

// Exec is the Runner that starts real processes. LC_ALL=C keeps pactl's
// labels in English, since its text output is translated.
func Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	return cmd.Output()
}

// Run probes the machine. It never fails: what it could not find is left
// empty and noted in Problems.
func Run(ctx context.Context, run Runner) Report {
	r := Report{ProbedAt: time.Now()}

	info, err := run(ctx, "pactl", "info")
	if err != nil {
		r.Problems = append(r.Problems, "pactl info failed, so no sound server is answering")
		return r
	}
	r.Server, r.ServerVersion = parseServer(info)

	clients, err := run(ctx, "pactl", "-f", "json", "list", "clients")
	if err == nil {
		manager, version, ok := parseClients(clients)
		r.PactlJSON = ok
		r.Manager, r.ManagerVersion = manager, version
	}
	if !r.PactlJSON {
		r.Problems = append(r.Problems, "pactl cannot print JSON, poweraudio needs pactl from PulseAudio 16 or newer")
	}

	if r.Server == ServerPipeWire && r.Manager != ManagerMediaSession {
		out, err := run(ctx, "pw-metadata", "-n", "sm-settings", "0")
		switch {
		case err != nil && r.Manager == ManagerWirePlumber && wirePlumber05(r):
			r.Problems = append(r.Problems, "pw-metadata is missing, install the PipeWire tools to read WirePlumber settings")
		case err == nil:
			r.Settings = parseMetadata(out)
		}
	}
	return r
}

// parseServer reads the Server Name and Server Version lines of `pactl info`.
// pipewire-pulse answers "PulseAudio (on PipeWire 1.6.9)" and a version of
// 15.0.0 that only describes the protocol it speaks, so the PipeWire version
// is taken from the name instead.
func parseServer(info []byte) (server, version string) {
	var name, ver string
	for _, line := range strings.Split(string(info), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "Server Name:"); ok {
			name = strings.TrimSpace(rest)
		}
		if rest, ok := strings.CutPrefix(line, "Server Version:"); ok {
			ver = strings.TrimSpace(rest)
		}
	}
	if name == "" {
		return "", ""
	}
	if i := strings.Index(name, "PipeWire"); i >= 0 {
		v := strings.TrimSpace(strings.TrimSuffix(name[i+len("PipeWire"):], ")"))
		return ServerPipeWire, v
	}
	return ServerPulseAudio, ver
}

// parseClients finds the session manager among the server's clients. ok is
// false when the output is not JSON, which is how an old pactl that ignores
// --format shows itself.
func parseClients(out []byte) (manager, version string, ok bool) {
	var clients []struct {
		Properties map[string]string `json:"properties"`
	}
	if err := json.Unmarshal(out, &clients); err != nil {
		return "", "", false
	}
	for _, c := range clients {
		name := c.Properties["application.name"]
		binary := c.Properties["application.process.binary"]
		switch {
		case name == ManagerWirePlumber || binary == "wireplumber":
			return ManagerWirePlumber, c.Properties["application.version"], true
		case binary == ManagerMediaSession || name == ManagerMediaSession:
			return ManagerMediaSession, c.Properties["application.version"], true
		}
	}
	return "", "", true
}

// parseMetadata reads `pw-metadata -n NAME 0`, whose lines look like
//
//	update: id:0 key:'bluetooth.autoswitch-to-headset-profile' value:'true' type:'Spa:String:JSON'
//
// A metadata object that does not exist prints nothing at all, which comes
// back as an empty map.
func parseMetadata(out []byte) map[string]string {
	values := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		key, ok := quoted(line, "key:'")
		if !ok {
			continue
		}
		value, _ := quoted(line, "value:'")
		values[key] = value
	}
	return values
}

// quoted returns the text between prefix and the next single quote.
func quoted(line, prefix string) (string, bool) {
	i := strings.Index(line, prefix)
	if i < 0 {
		return "", false
	}
	rest := line[i+len(prefix):]
	j := strings.IndexByte(rest, '\'')
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

// majorMinor reads "0.5.17" as 0 and 5.
func majorMinor(version string) (major, minor int, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(version), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return a, b, true
}
