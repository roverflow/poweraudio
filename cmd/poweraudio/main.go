package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/audio"
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

	run := runTUI
	if *daemonMode {
		run = runDaemon
	}
	if err := run(cfg, path); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func runDaemon(cfg config.Config, configPath string) error {
	backend, err := detectBackend(cfg.General.Backend)
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

func runTUI(cfg config.Config, _ string) error {
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

func detectBackend(preference string) (audio.Backend, error) {
	switch preference {
	case "pipewire":
		return audio.NewPipeWire(), nil
	case "pulseaudio":
		return audio.NewPulseAudio(), nil
	case "auto", "":
		if _, err := exec.LookPath("wpctl"); err == nil {
			if out, err := exec.Command("wpctl", "status").Output(); err == nil && len(out) > 0 {
				return audio.NewPipeWire(), nil
			}
		}
		if _, err := exec.LookPath("pactl"); err == nil {
			if out, err := exec.Command("pactl", "info").Output(); err == nil && len(out) > 0 {
				return audio.NewPulseAudio(), nil
			}
		}
		return nil, fmt.Errorf("no audio backend found: install PipeWire (wpctl) or PulseAudio (pactl)")
	default:
		return nil, fmt.Errorf("unknown backend: %s", preference)
	}
}
