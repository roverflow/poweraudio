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
	"github.com/roverflow/poweraudio/internal/version"
)

func main() {
	daemonMode := flag.Bool("daemon", false, "Run as background daemon")
	configPath := flag.String("config", "", "Config file path")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Usage = func() { cli.Usage(os.Stderr) }
	flag.Parse()

	if *showVersion {
		fmt.Println("poweraudio", version.String())
		return
	}

	// Resolve once so the daemon writes back to the file it read.
	path := config.ResolvePath(*configPath)

	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	args := flag.Args()
	command := ""
	if len(args) > 0 {
		command = args[0]
	}

	switch {
	case *daemonMode || command == "daemon":
		// The flag stays because the installed unit files pass it.
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
	log.Printf("poweraudio %s using %s backend", version.String(), backend.Name())

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
			// Exit zero, or Restart=on-failure restarts the unit every five
			// seconds while another daemon holds the socket.
			log.Print(err)
			return nil
		}
		return fmt.Errorf("ipc server: %w", err)
	}
	// Errors return instead of calling log.Fatal so this removes the socket.
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
