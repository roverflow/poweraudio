// Package cli is the non-interactive face of poweraudio. It turns one command
// line into one request to the daemon and one block of plain text on stdout,
// so the binary can be bound to a media key or polled by a status bar without
// anyone starting the terminal UI. Nothing it prints is coloured and every
// command that changes something prints what it changed, which is what makes
// the output safe to pipe into another program.
//
// The daemon owns all the state. Every command here reads a snapshot, decides
// what to do from that snapshot alone, and sends at most one request back.
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

// Exit codes. Two is reserved for a bad command line so a shell script can
// tell a typo from a device that is not there.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// daemonDown is the one message every command prints when nothing is
// listening on the socket. It names both ways of starting a daemon because
// the socket is equally likely to be missing on a machine that never
// installed the unit and on one where the unit is simply stopped.
const daemonDown = `poweraudio: daemon is not running (start it with "poweraudio daemon" or "systemctl --user start poweraudio")`

// Client is the part of ipc.Client the commands use. Taking an interface here
// is what lets the tests drive every command against a recorded fake instead
// of a live daemon and a real audio server.
type Client interface {
	Snapshot() (*ipc.Snapshot, error)
	Subscribe(ctx context.Context) (<-chan ipc.Snapshot, error)
	SetDefault(deviceID string) error
	SetVolume(deviceID string, percent int) error
	ToggleMute(deviceID string) error
	ReloadConfig() error
}

// Run executes one command and returns the process exit code. args is the
// command line with the leading flags already removed, so args[0] is the
// command name.
func Run(args []string, client *ipc.Client, stdout, stderr io.Writer) int {
	return run(args, client, stdout, stderr)
}

// run is Run over the interface, so tests reach it without a socket.
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

// usageError marks a bad command line. It is the only thing separating an
// exit code of two from the one a failed request gets.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error {
	return usageError{msg: fmt.Sprintf(format, a...)}
}

func isUsageError(err error) bool {
	var ue usageError
	return errors.As(err, &ue)
}

// isUnreachable reports whether err means nothing answered on the socket, as
// opposed to a daemon that answered with a failure. Dialling a missing or
// dead unix socket fails with a net.OpError, so that is what separates the
// two cases and decides whether the user is told to start a daemon.
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
  set <query>                    make a device the default output
  next                           switch to the next available device
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
`

// Usage writes the command reference. main uses it for the top level flag
// parser too, so a bad flag and a bad command print the same page.
func Usage(w io.Writer) {
	fmt.Fprint(w, usageText)
}
