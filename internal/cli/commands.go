package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"syscall"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/ipc"
)

func cmdList(args []string, client Client, out io.Writer) error {
	positional, opts, err := parseOptions("list", args, flagSpec{json: true})
	if err != nil {
		return err
	}
	if err := expectNoArgs("list", positional); err != nil {
		return err
	}

	snap, err := client.Snapshot()
	if err != nil {
		return err
	}

	if opts.json {
		devices := snap.Devices
		if devices == nil {
			// An empty list is easier to consume than null for anything
			// looping over the output.
			devices = []audio.Device{}
		}
		return writeJSON(out, devices)
	}

	_, err = io.WriteString(out, renderList(snap))
	return err
}

func cmdStatus(args []string, client Client, out io.Writer) error {
	positional, opts, err := parseOptions("status", args, flagSpec{json: true})
	if err != nil {
		return err
	}
	if err := expectNoArgs("status", positional); err != nil {
		return err
	}

	snap, err := client.Snapshot()
	if err != nil {
		return err
	}

	if opts.json {
		return writeJSON(out, snap)
	}

	_, err = io.WriteString(out, renderStatus(snap, time.Now()))
	return err
}

func cmdSet(args []string, client Client, out io.Writer) error {
	positional, _, err := parseOptions("set", args, flagSpec{})
	if err != nil {
		return err
	}
	query, err := expectOneArg("set", "a device query", positional)
	if err != nil {
		return err
	}

	snap, err := client.Snapshot()
	if err != nil {
		return err
	}
	dev, err := findDevice(snap.Devices, query)
	if err != nil {
		return err
	}
	if err := client.SetDefault(dev.ID); err != nil {
		return err
	}

	fmt.Fprintln(out, dev.Name)
	return nil
}

func cmdNext(args []string, client Client, out io.Writer) error {
	positional, _, err := parseOptions("next", args, flagSpec{})
	if err != nil {
		return err
	}
	if err := expectNoArgs("next", positional); err != nil {
		return err
	}

	snap, err := client.Snapshot()
	if err != nil {
		return err
	}
	dev, err := nextDevice(snap.Devices)
	if err != nil {
		return err
	}
	if err := client.SetDefault(dev.ID); err != nil {
		return err
	}

	fmt.Fprintln(out, dev.Name)
	return nil
}

func cmdVolume(args []string, client Client, out io.Writer) error {
	positional, opts, err := parseOptions("volume", args, flagSpec{device: true})
	if err != nil {
		return err
	}
	arg, err := expectOneArg("volume", "a level such as 50, +10 or -10", positional)
	if err != nil {
		return err
	}
	spec, err := parseVolumeSpec(arg)
	if err != nil {
		return err
	}

	snap, err := client.Snapshot()
	if err != nil {
		return err
	}
	dev, err := targetDevice(snap, opts.device)
	if err != nil {
		return err
	}

	percent := spec.apply(volumePercent(dev.Volume))
	if err := client.SetVolume(dev.ID, percent); err != nil {
		return err
	}

	fmt.Fprintf(out, "%d%%\n", percent)
	return nil
}

func cmdMute(args []string, client Client, out io.Writer) error {
	positional, opts, err := parseOptions("mute", args, flagSpec{device: true})
	if err != nil {
		return err
	}
	if err := expectNoArgs("mute", positional); err != nil {
		return err
	}

	snap, err := client.Snapshot()
	if err != nil {
		return err
	}
	dev, err := targetDevice(snap, opts.device)
	if err != nil {
		return err
	}
	if err := client.ToggleMute(dev.ID); err != nil {
		return err
	}

	// The daemon owns the state, so the new state is read back rather than
	// assumed. Printing the opposite of what we saw would lie whenever the
	// backend refused the change.
	muted := !dev.Muted
	if after, err := client.Snapshot(); err == nil {
		if updated := deviceByID(after.Devices, dev.ID); updated != nil {
			muted = updated.Muted
		}
	}

	if muted {
		fmt.Fprintln(out, "muted")
	} else {
		fmt.Fprintln(out, "unmuted")
	}
	return nil
}

func cmdWatch(args []string, client Client, out io.Writer) error {
	positional, opts, err := parseOptions("watch", args, flagSpec{json: true})
	if err != nil {
		return err
	}
	if err := expectNoArgs("watch", positional); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stream, err := client.Subscribe(ctx)
	if err != nil {
		return err
	}
	return watch(ctx, stream, out, opts.json)
}

// watch prints one line per change until the stream ends. Repeated lines are
// dropped in text mode because the daemon sends a snapshot for every event it
// logs, and a status bar only wants to hear about the ones that change what
// it draws. The json mode keeps every snapshot, since a program reading it
// may care about the parts the line leaves out.
func watch(ctx context.Context, stream <-chan ipc.Snapshot, out io.Writer, asJSON bool) error {
	last := ""
	for snap := range stream {
		line := watchLine(snap)
		if asJSON {
			data, err := json.Marshal(snap)
			if err != nil {
				return fmt.Errorf("encoding json: %w", err)
			}
			line = string(data)
		} else if line == last {
			continue
		}
		last = line

		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}

	// A closed channel is either the signal handler doing its job or the
	// daemon going away under us, and only the second one is a failure.
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("daemon went away")
}

func cmdReload(args []string, client Client, out io.Writer) error {
	positional, _, err := parseOptions("reload", args, flagSpec{})
	if err != nil {
		return err
	}
	if err := expectNoArgs("reload", positional); err != nil {
		return err
	}

	if err := client.ReloadConfig(); err != nil {
		return err
	}

	fmt.Fprintln(out, "config reloaded")
	return nil
}
