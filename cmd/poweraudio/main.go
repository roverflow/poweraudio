package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/cli"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/daemon"
	"github.com/roverflow/poweraudio/internal/ipc"
	"github.com/roverflow/poweraudio/internal/tui"
)

var version = "dev"

func main() {
	daemonMode := flag.Bool("daemon", false, "Run as background daemon")
	configPath := flag.String("config", "", "Config file path")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Usage = func() { cli.Usage(os.Stderr) }
	flag.Parse()

	if *showVersion {
		fmt.Println("poweraudio", version)
		return
	}

	// Resolve once, so the daemon writes changes back to the file it read
	// rather than to the default location.
	path := config.ResolvePath(*configPath)

	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// flag.Parse stops at the first argument that is not a flag, so anything
	// left is a command and its own arguments.
	args := flag.Args()
	command := ""
	if len(args) > 0 {
		command = args[0]
	}

	switch {
	case *daemonMode || command == "daemon":
		// The flag is kept because the installed unit files pass it.
		if err := runDaemon(cfg, path); err != nil {
			log.Print(err)
			os.Exit(1)
		}
	case command == "":
		if err := runTUI(cfg); err != nil {
			log.Print(err)
			os.Exit(1)
		}
	default:
		client := ipc.NewClient(cfg.Daemon.SocketPath)
		os.Exit(cli.Run(args, client, os.Stdout, os.Stderr))
	}
}

func runDaemon(cfg config.Config, configPath string) error {
	backend, err := audio.Detect(cfg.General.Backend)
	if err != nil {
		return fmt.Errorf("audio backend: %w", err)
	}
	log.Printf("using %s backend", backend.Name())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("shutting down...")
		cancel()
	}()

	d := daemon.New(cfg, backend, configPath)

	srv := daemon.NewServer(cfg.Daemon.SocketPath, d)
	if err := srv.Start(ctx); err != nil {
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			// Losing the socket to another daemon is a normal outcome, not a
			// failure, so this returns nil and exits zero. The unit file sets
			// Restart=on-failure with RestartSec=5, so exiting non-zero here
			// restarted the unit every five seconds for as long as a session
			// daemon held the socket.
			log.Print(err)
			return nil
		}
		return fmt.Errorf("ipc server: %w", err)
	}
	// Returning rather than calling log.Fatal keeps this reachable, so the
	// socket file does not outlive the daemon.
	defer srv.Close()
	log.Printf("listening on %s", cfg.Daemon.SocketPath)

	if err := d.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("daemon: %w", err)
	}
	return nil
}

func runTUI(cfg config.Config) error {
	client := ipc.NewClient(cfg.Daemon.SocketPath)

	if !client.Ping() {
		setup := tui.NewSetupModel(client)
		result, err := tea.NewProgram(setup).Run()
		if err != nil {
			return fmt.Errorf("setup: %w", err)
		}

		sm, ok := result.(tui.SetupModel)
		if !ok || !sm.Done() {
			return nil
		}
	}

	if _, err := tea.NewProgram(tui.NewModel(client)).Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
