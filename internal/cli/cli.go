// Package cli runs one poweraudio command against the daemon and prints
// plain text, so the binary can sit behind a media key or a status bar.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"

	"github.com/roverflow/poweraudio/internal/ipc"
)

// Exit codes. exitUsage lets a script tell a typo from a missing device.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const daemonDown = `poweraudio: daemon is not running (start it with "poweraudio daemon" or "systemctl --user start poweraudio")`

// Client is the part of ipc.Client the commands use, so tests can fake it.
type Client interface {
	Snapshot() (*ipc.Snapshot, error)
	Subscribe(ctx context.Context) (<-chan ipc.Snapshot, error)
	SetDefault(deviceID string, notify bool) error
	SetVolume(deviceID string, percent int) error
	ToggleMute(deviceID string) error
	ReloadConfig() error
}

// Run executes one command and returns the exit code. args[0] is the
// command name, with leading flags already removed.
func Run(args []string, client *ipc.Client, stdout, stderr io.Writer) int {
	return run(args, client, stdout, stderr)
}

func run(args []string, client Client, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		Usage(stderr)
		return exitUsage
	}

	switch args[0] {
	case "help", "-h", "--help":
		Usage(stdout)
		return exitOK
	}

	err := dispatch(args[0], args[1:], client, stdout)
	switch {
	case err == nil:
		return exitOK
	case isUsageError(err):
		fmt.Fprintf(stderr, "poweraudio: %v\n", err)
		Usage(stderr)
		return exitUsage
	case isUnreachable(err):
		fmt.Fprintln(stderr, daemonDown)
		return exitError
	default:
		fmt.Fprintf(stderr, "poweraudio: %v\n", err)
		return exitError
	}
}

func dispatch(cmd string, args []string, client Client, out io.Writer) error {
	switch cmd {
	case "list":
		return cmdList(args, client, out)
	case "status":
		return cmdStatus(args, client, out)
	case "set":
		return cmdSet(args, client, out)
	case "next":
		return cmdNext(args, client, out)
	case "volume":
		return cmdVolume(args, client, out)
	case "mute":
		return cmdMute(args, client, out)
	case "watch":
		return cmdWatch(args, client, out)
	case "reload":
		return cmdReload(args, client, out)
	default:
		return usagef("unknown command %q", cmd)
	}
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error {
	return usageError{msg: fmt.Sprintf(format, a...)}
}

func isUsageError(err error) bool {
	var ue usageError
	return errors.As(err, &ue)
}

// isUnreachable reports whether nothing answered on the socket, as opposed
// to a daemon that answered with an error.
func isUnreachable(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	return errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET)
}

const usageText = `poweraudio switches the default audio output when your headphones connect.

usage:
  poweraudio [--config PATH] [--version] [--daemon] [<command> [args]]

Without a command poweraudio opens the terminal UI.

commands:
  list [--json]                  output devices, one per line
  status [--json]                default device, backend, uptime, recent events
  set <query> [--notify]         make a device the default output
  next [--notify]                switch to the next device that can play
  volume <+N|-N|N> [--device Q]  set or adjust the volume, 0 to 150 percent
  mute [--device Q]              toggle mute
  watch [--json]                 print a line every time the default changes
  reload                         make the daemon re-read its config file
  daemon                         run the daemon in the foreground
  help                           print this text

flags:
  --config PATH                  read and write this config file
  --daemon                       run the daemon in the foreground
  --version                      print the version and exit

A query matches a device id, name, description or MAC address, either exactly
or as a case-insensitive substring.

--notify shows a desktop notification naming the new output, for set and next
bound to a key where there is no terminal to print to.
`

// Usage writes the command reference. main also prints it for bad flags.
func Usage(w io.Writer) {
	fmt.Fprint(w, usageText)
}
