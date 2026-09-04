package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	General       GeneralConfig       `toml:"general" json:"general"`
	Switching     SwitchingConfig     `toml:"switching" json:"switching"`
	Notifications NotificationsConfig `toml:"notifications" json:"notifications"`
	Daemon        DaemonConfig        `toml:"daemon" json:"daemon"`
	TUI           TUIConfig           `toml:"tui" json:"tui"`
	Priority      []PriorityEntry     `toml:"priority" json:"priority"`
}

type GeneralConfig struct {
	Backend  string `toml:"backend" json:"backend"`
	LogLevel string `toml:"log_level" json:"log_level"`
	LogFile  string `toml:"log_file" json:"log_file"`
}

type SwitchingConfig struct {
	OnConnect     string `toml:"on_connect" json:"on_connect"`
	OnDisconnect  string `toml:"on_disconnect" json:"on_disconnect"`
	SwitchDelayMs int    `toml:"switch_delay_ms" json:"switch_delay_ms"`
}

type NotificationsConfig struct {
	Enabled        bool `toml:"enabled" json:"enabled"`
	OnDeviceChange bool `toml:"on_device_change" json:"on_device_change"`
}

type DaemonConfig struct {
	SocketPath string `toml:"socket_path"`
}

type TUIConfig struct {
	ShowVolume bool `toml:"show_volume"`
}

type PriorityEntry struct {
	Match string `toml:"match" json:"match"`
	Type  string `toml:"type,omitempty" json:"type,omitempty"`
}

func DefaultConfig() Config {
	socketPath := filepath.Join(xdgRuntimeDir(), "poweraudio.sock")
	return Config{
		General: GeneralConfig{
			Backend:  "auto",
			LogLevel: "info",
		},
		Switching: SwitchingConfig{
			OnConnect:     "always",
			OnDisconnect:  "priority",
			SwitchDelayMs: 500,
		},
		Notifications: NotificationsConfig{
			Enabled:        true,
			OnDeviceChange: true,
		},
		Daemon: DaemonConfig{
			SocketPath: socketPath,
		},
		TUI: TUIConfig{
			ShowVolume: true,
		},
	}
}

func DefaultPath() string {
	return filepath.Join(xdgConfigHome(), "poweraudio", "config.toml")
}

// ResolvePath fills in the default location for an empty path, so callers can
// record where the config actually came from and write back to the same file.
func ResolvePath(path string) string {
	if path == "" {
		return DefaultPath()
	}
	return path
}

func Load(path string) (Config, error) {
	path = ResolvePath(path)

	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if writeErr := writeDefault(path, cfg); writeErr != nil {
				return cfg, fmt.Errorf("creating default config: %w", writeErr)
			}
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config: %w", err)
	}

	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Daemon.SocketPath == "" {
		cfg.Daemon.SocketPath = filepath.Join(xdgRuntimeDir(), "poweraudio.sock")
	}

	return cfg, nil
}

func Save(path string, cfg Config) error {
	return write(ResolvePath(path), cfg, "")
}

func writeDefault(path string, cfg Config) error {
	return write(path, cfg, "# poweraudio configuration\n\n")
}

// write encodes cfg beside the target and renames it into place. Truncating
// the real file first meant a crash mid-write left a half-written config that
// the next start refused to parse.
func write(path string, cfg Config, header string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".config.toml.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if err := encode(f, cfg, header); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func encode(f *os.File, cfg Config, header string) error {
	if header != "" {
		if _, err := fmt.Fprint(f, header); err != nil {
			return err
		}
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return err
	}
	return f.Sync()
}

func xdgConfigHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

func xdgRuntimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}
