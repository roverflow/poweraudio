package probe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner serves testdata files. An unlisted command fails.
func fakeRunner(t *testing.T, files map[string]string) Runner {
	t.Helper()
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := strings.Join(append([]string{name}, args...), " ")
		file, ok := files[key]
		if !ok {
			return nil, errors.New("exec: not found")
		}
		if file == "" {
			return nil, nil
		}
		data, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatalf("reading fixture %s: %v", file, err)
		}
		return data, nil
	}
}

const (
	cmdInfo     = "pactl info"
	cmdClients  = "pactl -f json list clients"
	cmdSettings = "pw-metadata -n sm-settings 0"
)

func TestWirePlumber05(t *testing.T) {
	r := Run(context.Background(), fakeRunner(t, map[string]string{
		cmdInfo:     "info-pipewire.txt",
		cmdClients:  "clients-wireplumber-0.5.json",
		cmdSettings: "sm-settings.txt",
	}))

	if r.Server != ServerPipeWire || r.ServerVersion != "1.6.9" {
		t.Errorf("server = %q %q, want PipeWire 1.6.9 taken from the server name", r.Server, r.ServerVersion)
	}
	if r.Manager != ManagerWirePlumber || r.ManagerVersion != "0.5.17" {
		t.Errorf("manager = %q %q, want WirePlumber 0.5.17", r.Manager, r.ManagerVersion)
	}
	if r.Tier() != TierWirePlumber05 {
		t.Errorf("tier = %s, want %s", r.Tier(), TierWirePlumber05)
	}
	if !r.PactlJSON {
		t.Error("pactl printed JSON, but the report says it cannot")
	}
	if !r.HasSetting("bluetooth.autoswitch-to-headset-profile") {
		t.Errorf("settings are missing a key the fixture has: %v", r.Settings)
	}
	if got := r.Settings["bluetooth.profile-preference"]; got != `"quality"` {
		t.Errorf("profile-preference = %q, want the raw JSON value", got)
	}
	if r.HasSetting("no.such.setting") {
		t.Error("reported a setting this version does not have")
	}
	if got, want := r.Summary(), "PipeWire 1.6.9, WirePlumber 0.5.17"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if len(r.Problems) != 0 {
		t.Errorf("problems on a healthy machine: %v", r.Problems)
	}
}

func TestWirePlumber04HasNoSettings(t *testing.T) {
	// pw-metadata prints nothing for metadata that does not exist.
	r := Run(context.Background(), fakeRunner(t, map[string]string{
		cmdInfo:     "info-pipewire.txt",
		cmdClients:  "clients-wireplumber-0.4.json",
		cmdSettings: "",
	}))
	if r.Tier() != TierWirePlumber04 {
		t.Errorf("tier = %s, want %s", r.Tier(), TierWirePlumber04)
	}
	if len(r.Settings) != 0 {
		t.Errorf("0.4 reported live settings: %v", r.Settings)
	}
	if len(r.Problems) != 0 {
		t.Errorf("an empty metadata read is normal on 0.4, got problems %v", r.Problems)
	}
}

func TestMissingVersionFallsBackOnSettings(t *testing.T) {
	r := Report{Server: ServerPipeWire, Manager: ManagerWirePlumber}
	if r.Tier() != TierWirePlumber04 {
		t.Errorf("no version and no settings: tier = %s, want 0.4", r.Tier())
	}
	r.Settings = map[string]string{"node.restore-default-targets": "true"}
	if r.Tier() != TierWirePlumber05 {
		t.Errorf("settings metadata exists only on 0.5: tier = %s", r.Tier())
	}
}

func TestMediaSession(t *testing.T) {
	r := Run(context.Background(), fakeRunner(t, map[string]string{
		cmdInfo:    "info-pipewire.txt",
		cmdClients: "clients-media-session.json",
	}))
	if r.Manager != ManagerMediaSession {
		t.Errorf("manager = %q, want pipewire-media-session", r.Manager)
	}
	if r.Tier() != TierPipeWire {
		t.Errorf("tier = %s, want %s", r.Tier(), TierPipeWire)
	}
}

func TestPulseAudio(t *testing.T) {
	r := Run(context.Background(), fakeRunner(t, map[string]string{
		cmdInfo:    "info-pulseaudio.txt",
		cmdClients: "clients-pulseaudio.json",
	}))
	if r.Server != ServerPulseAudio || r.ServerVersion != "16.1" {
		t.Errorf("server = %q %q, want PulseAudio 16.1", r.Server, r.ServerVersion)
	}
	if r.Tier() != TierPulseAudio {
		t.Errorf("tier = %s, want %s", r.Tier(), TierPulseAudio)
	}
	if got := r.Summary(); got != "PulseAudio 16.1" {
		t.Errorf("summary = %q", got)
	}
}

func TestNoServer(t *testing.T) {
	r := Run(context.Background(), fakeRunner(t, nil))
	if r.Tier() != TierUnknown {
		t.Errorf("tier = %s, want unknown", r.Tier())
	}
	if len(r.Problems) == 0 {
		t.Error("no sound server and no problem reported")
	}
}

func TestOldPactlWithoutJSON(t *testing.T) {
	r := Run(context.Background(), fakeRunner(t, map[string]string{
		cmdInfo:    "info-pulseaudio.txt",
		cmdClients: "info-pulseaudio.txt",
	}))
	if r.PactlJSON {
		t.Error("text output was taken for JSON")
	}
	if len(r.Problems) == 0 || !strings.Contains(r.Problems[0], "PulseAudio 16") {
		t.Errorf("problems = %v, want one naming the pactl it needs", r.Problems)
	}
}

func TestMissingPwMetadataIsReported(t *testing.T) {
	r := Run(context.Background(), fakeRunner(t, map[string]string{
		cmdInfo:    "info-pipewire.txt",
		cmdClients: "clients-wireplumber-0.5.json",
	}))
	if len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "pw-metadata") {
		t.Errorf("problems = %v, want the missing pw-metadata named", r.Problems)
	}
}
