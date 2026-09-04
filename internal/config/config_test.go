package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePath(t *testing.T) {
	if got := ResolvePath(""); got != DefaultPath() {
		t.Errorf("ResolvePath(\"\") = %q, want the default path", got)
	}
	if got := ResolvePath("/tmp/custom.toml"); got != "/tmp/custom.toml" {
		t.Errorf("ResolvePath overrode an explicit path: %q", got)
	}
}

func TestLoadWritesDefaultWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Switching.OnConnect != "always" {
		t.Errorf("on_connect = %q, want the default", cfg.Switching.OnConnect)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("default config was not written: %v", err)
	}
	if !strings.HasPrefix(string(data), "# poweraudio configuration") {
		t.Errorf("generated config lost its header:\n%s", data)
	}
}

// Save used to ignore the path it was given and always write to the default
// location, so a daemon started with --config edited the wrong file.
func TestSaveHonoursAnExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.toml")

	cfg := DefaultConfig()
	cfg.Switching.OnConnect = "never"
	cfg.Switching.OnDisconnect = "previous"
	cfg.Priority = []PriorityEntry{
		{Match: "JBL Tune 520BT", Type: "bluetooth"},
		{Match: "Built-in Audio Analog Stereo"},
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Switching.OnConnect != "never" || got.Switching.OnDisconnect != "previous" {
		t.Errorf("switching did not survive the round trip: %+v", got.Switching)
	}
	if len(got.Priority) != 2 || got.Priority[0].Match != "JBL Tune 520BT" {
		t.Errorf("priorities did not survive the round trip: %+v", got.Priority)
	}
	if got.Priority[0].Type != "bluetooth" {
		t.Errorf("priority type did not survive: %q", got.Priority[0].Type)
	}
}

// The write goes to a temp file and is renamed, so a crash cannot leave a
// half-written config behind. Nothing should be left over afterwards.
func TestSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	for i := 0; i < 3; i++ {
		if err := Save(path, DefaultConfig()); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want just config.toml", names)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("config mode = %o, want 644", perm)
	}
}

func TestLoadReportsBadSyntax(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("this is not = = toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load accepted a config it cannot parse")
	}
}

func TestLoadFillsInTheSocketPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[daemon]\nsocket_path = \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.Daemon.SocketPath, "poweraudio.sock") {
		t.Errorf("socket path = %q, want a default ending in poweraudio.sock", cfg.Daemon.SocketPath)
	}
}
