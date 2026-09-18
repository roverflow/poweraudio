package cli

import (
	"bytes"
	"strings"
	"testing"
)

// exec runs one command line against a fake daemon and returns what the user
// would have seen.
func exec(client Client, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, client, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunArgumentParsing(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
		wantOut  string
	}{
		{
			name:     "no command",
			args:     nil,
			wantCode: exitUsage,
			wantErr:  "usage:",
		},
		{
			name:     "help goes to stdout",
			args:     []string{"help"},
			wantCode: exitOK,
			wantOut:  "usage:",
		},
		{
			name:     "short help goes to stdout too",
			args:     []string{"-h"},
			wantCode: exitOK,
			wantOut:  "usage:",
		},
		{
			name:     "unknown command",
			args:     []string{"lsit"},
			wantCode: exitUsage,
			wantErr:  `unknown command "lsit"`,
		},
		{
			name:     "unknown flag",
			args:     []string{"list", "--verbose"},
			wantCode: exitUsage,
			wantErr:  `list: unknown flag "--verbose"`,
		},
		{
			name:     "flag a command does not take",
			args:     []string{"set", "--json", "jbl"},
			wantCode: exitUsage,
			wantErr:  `set: unknown flag "--json"`,
		},
		{
			name:     "list takes no arguments",
			args:     []string{"list", "jbl"},
			wantCode: exitUsage,
			wantErr:  "list takes no arguments",
		},
		{
			name:     "set without a query",
			args:     []string{"set"},
			wantCode: exitUsage,
			wantErr:  "set needs a device query",
		},
		{
			name:     "set with two queries",
			args:     []string{"set", "jbl", "tune"},
			wantCode: exitUsage,
			wantErr:  "set takes one argument, got 2",
		},
		{
			name:     "next takes no arguments",
			args:     []string{"next", "jbl"},
			wantCode: exitUsage,
			wantErr:  "next takes no arguments",
		},
		{
			name:     "volume without a level",
			args:     []string{"volume"},
			wantCode: exitUsage,
			wantErr:  "volume needs a level",
		},
		{
			name:     "volume with a word",
			args:     []string{"volume", "loud"},
			wantCode: exitUsage,
			wantErr:  `invalid volume "loud"`,
		},
		{
			name:     "device flag without a value",
			args:     []string{"volume", "50", "--device"},
			wantCode: exitUsage,
			wantErr:  "volume: --device needs a device query",
		},
		{
			name:     "empty device flag value",
			args:     []string{"mute", "--device="},
			wantCode: exitUsage,
			wantErr:  "mute: --device needs a device query",
		},
		{
			name:     "reload takes no flags",
			args:     []string{"reload", "--json"},
			wantCode: exitUsage,
			wantErr:  `reload: unknown flag "--json"`,
		},
		{
			name:     "list",
			args:     []string{"list"},
			wantCode: exitOK,
			wantOut:  "JBL Tune 520BT",
		},
		{
			name:     "list json",
			args:     []string{"list", "--json"},
			wantCode: exitOK,
			wantOut:  `"id": "bluez_output.3C_B0_ED_3A_2C_42.1"`,
		},
		{
			name:     "status",
			args:     []string{"status"},
			wantCode: exitOK,
			wantOut:  "pipewire",
		},
		{
			name:     "status json",
			args:     []string{"status", "--json"},
			wantCode: exitOK,
			wantOut:  `"backend": "pipewire"`,
		},
		{
			name:     "set",
			args:     []string{"set", "razer"},
			wantCode: exitOK,
			wantOut:  "Razer Barracuda X\n",
		},
		{
			name:     "set by MAC address",
			args:     []string{"set", "3c:b0:ed:3a:2c:42"},
			wantCode: exitOK,
			wantOut:  "JBL Tune 520BT\n",
		},
		{
			name:     "set a device that is not there",
			args:     []string{"set", "sennheiser"},
			wantCode: exitError,
			wantErr:  `no device matches "sennheiser"`,
		},
		{
			name:     "next",
			args:     []string{"next"},
			wantCode: exitOK,
			wantOut:  "Built-in Audio Analog Stereo\n",
		},
		{
			name:     "absolute volume",
			args:     []string{"volume", "50"},
			wantCode: exitOK,
			wantOut:  "50%\n",
		},
		{
			name:     "relative volume up",
			args:     []string{"volume", "+10"},
			wantCode: exitOK,
			wantOut:  "90%\n",
		},
		{
			name:     "relative volume down",
			args:     []string{"volume", "-10"},
			wantCode: exitOK,
			wantOut:  "70%\n",
		},
		{
			name:     "volume on a named device",
			args:     []string{"volume", "+10", "--device", "built-in"},
			wantCode: exitOK,
			wantOut:  "55%\n",
		},
		{
			name:     "volume with a trailing percent sign",
			args:     []string{"volume", "33%"},
			wantCode: exitOK,
			wantOut:  "33%\n",
		},
		{
			name:     "reload",
			args:     []string{"reload"},
			wantCode: exitOK,
			wantOut:  "config reloaded\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := exec(newFake(), c.args...)
			if code != c.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, c.wantCode, stderr)
			}
			if c.wantOut != "" && !strings.Contains(stdout, c.wantOut) {
				t.Errorf("stdout = %q, want it to contain %q", stdout, c.wantOut)
			}
			if c.wantErr != "" && !strings.Contains(stderr, c.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, c.wantErr)
			}
			if c.wantCode == exitOK && c.wantErr == "" && stderr != "" {
				t.Errorf("stderr = %q, want nothing on a successful command", stderr)
			}
		})
	}
}

func TestRunSendsTheRightRequests(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		calls []string
	}{
		{
			name:  "list only reads",
			args:  []string{"list"},
			calls: []string{"snapshot"},
		},
		{
			name:  "set resolves the query to an id",
			args:  []string{"set", "jbl"},
			calls: []string{"snapshot", "set_default bluez_output.3C_B0_ED_3A_2C_42.1"},
		},
		{
			name:  "next skips the unavailable device",
			args:  []string{"next"},
			calls: []string{"snapshot", "set_default alsa_output.pci-0000_00_1f.3.analog-stereo"},
		},
		{
			name:  "volume acts on the default device",
			args:  []string{"volume", "+5"},
			calls: []string{"snapshot", "set_volume bluez_output.3C_B0_ED_3A_2C_42.1 85"},
		},
		{
			name:  "volume clamps at the top",
			args:  []string{"volume", "+100"},
			calls: []string{"snapshot", "set_volume bluez_output.3C_B0_ED_3A_2C_42.1 150"},
		},
		{
			name:  "mute reads the new state back",
			args:  []string{"mute"},
			calls: []string{"snapshot", "toggle_mute bluez_output.3C_B0_ED_3A_2C_42.1", "snapshot"},
		},
		{
			name:  "reload does not need a snapshot",
			args:  []string{"reload"},
			calls: []string{"reload_config"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newFake()
			if code, _, stderr := exec(fake, c.args...); code != exitOK {
				t.Fatalf("exit code = %d, stderr: %s", code, stderr)
			}
			if got := strings.Join(fake.calls, "; "); got != strings.Join(c.calls, "; ") {
				t.Errorf("calls = %q, want %q", got, c.calls)
			}
		})
	}
}

func TestMutePrintsTheStateTheDaemonReports(t *testing.T) {
	before := fixture()
	after := fixture()
	after.Devices[1].Muted = true

	code, stdout, stderr := exec(newFake(before, after), "mute")
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	if stdout != "muted\n" {
		t.Errorf("stdout = %q, want %q", stdout, "muted\n")
	}

	// A daemon that refused the change keeps reporting the old state, and the
	// command has to say so rather than echo the toggle it asked for.
	code, stdout, _ = exec(newFake(before, before), "mute")
	if code != exitOK || stdout != "unmuted\n" {
		t.Errorf("refused toggle printed %q with code %d, want %q", stdout, code, "unmuted\n")
	}
}

func TestDaemonNotRunning(t *testing.T) {
	commands := [][]string{
		{"list"},
		{"status"},
		{"set", "jbl"},
		{"next"},
		{"volume", "+5"},
		{"mute"},
		{"watch"},
		{"reload"},
	}

	for _, args := range commands {
		t.Run(args[0], func(t *testing.T) {
			code, stdout, stderr := exec(downFake(), args...)
			if code != exitError {
				t.Errorf("exit code = %d, want %d", code, exitError)
			}
			if stderr != daemonDown+"\n" {
				t.Errorf("stderr = %q, want %q", stderr, daemonDown+"\n")
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
		})
	}
}

func TestParseOptions(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		spec       flagSpec
		positional []string
		opts       options
		wantErr    bool
	}{
		{
			name:       "negative numbers are not flags",
			args:       []string{"-10"},
			spec:       flagSpec{device: true},
			positional: []string{"-10"},
		},
		{
			name:       "device flag with a separate value",
			args:       []string{"+10", "--device", "jbl"},
			spec:       flagSpec{device: true},
			positional: []string{"+10"},
			opts:       options{device: "jbl"},
		},
		{
			name:       "device flag with an equals sign",
			args:       []string{"--device=jbl", "+10"},
			spec:       flagSpec{device: true},
			positional: []string{"+10"},
			opts:       options{device: "jbl"},
		},
		{
			name: "json flag",
			args: []string{"--json"},
			spec: flagSpec{json: true},
			opts: options{json: true},
		},
		{
			name:       "double dash ends the flags",
			args:       []string{"--", "--json"},
			spec:       flagSpec{json: true},
			positional: []string{"--json"},
		},
		{
			name:    "flag outside the spec",
			args:    []string{"--json"},
			spec:    flagSpec{device: true},
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			positional, opts, err := parseOptions("test", c.args, c.spec)
			if c.wantErr {
				if err == nil {
					t.Fatal("parseOptions succeeded, want an error")
				}
				if !isUsageError(err) {
					t.Errorf("error %v is not a usage error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOptions: %v", err)
			}
			if strings.Join(positional, ",") != strings.Join(c.positional, ",") {
				t.Errorf("positional = %q, want %q", positional, c.positional)
			}
			if opts != c.opts {
				t.Errorf("options = %+v, want %+v", opts, c.opts)
			}
		})
	}
}
