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

	tea "github.com/charmbracelet/bubbletea/v2"
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
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if *daemonMode {
		runDaemon(cfg)
	} else {
		runTUI(cfg)
	}
}

func runDaemon(cfg config.Config) {
	backend, err := detectBackend(cfg.General.Backend)
	if err != nil {
		log.Fatalf("audio backend: %v", err)
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

	d := daemon.New(cfg, backend)

	srv := daemon.NewServer(cfg.Daemon.SocketPath, d)
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("ipc server: %v", err)
	}
	defer srv.Close()
	log.Printf("listening on %s", cfg.Daemon.SocketPath)

	if err := d.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("daemon: %v", err)
	}
}

func runTUI(cfg config.Config) {
	client := ipc.NewClient(cfg.Daemon.SocketPath)

	if !client.Ping() {
		setup := tui.NewSetupModel(client)
		sp := tea.NewProgram(setup, tea.WithAltScreen())
		result, err := sp.Run()
		if err != nil {
			log.Fatalf("setup: %v", err)
		}

		sm, ok := result.(tui.SetupModel)
		if !ok || !sm.Done() {
			return
		}
	}

	m := tui.NewModel(client)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
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
