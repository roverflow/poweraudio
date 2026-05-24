package daemon

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/bluetooth"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
)

type Daemon struct {
	cfg       config.Config
	backend   audio.Backend
	btMonitor *bluetooth.Monitor

	mu         sync.RWMutex
	devices    []audio.Device
	previousID string
	events     []ipc.EventLog
	startTime  time.Time

	ipcRequests chan IPCRequest
}

type IPCRequest struct {
	Request  Request
	Response chan Response
}

func New(cfg config.Config, backend audio.Backend) *Daemon {
	return &Daemon{
		cfg:         cfg,
		backend:     backend,
		startTime:   time.Now(),
		ipcRequests: make(chan IPCRequest, 16),
	}
}

func (d *Daemon) IPCChannel() chan<- IPCRequest {
	return d.ipcRequests
}

func (d *Daemon) Run(ctx context.Context) error {
	d.logEvent("daemon started with %s backend", d.backend.Name())

	d.refreshDevices(ctx)

	var btEvents <-chan bluetooth.Event
	btMon, err := bluetooth.NewMonitor()
	if err != nil {
		d.logEvent("bluetooth monitoring unavailable: %v", err)
	} else {
		d.btMonitor = btMon
		defer btMon.Close()
		ch, err := btMon.Subscribe(ctx)
		if err != nil {
			d.logEvent("bluetooth subscribe failed: %v", err)
		} else {
			btEvents = ch
			d.logEvent("bluetooth monitoring active")
		}
	}

	audioEvents, err := d.backend.SubscribeEvents(ctx)
	if err != nil {
		d.logEvent("audio event subscription failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			d.logEvent("daemon stopping")
			return ctx.Err()

		case ev, ok := <-btEvents:
			if !ok {
				btEvents = nil
				continue
			}
			d.handleBluetoothEvent(ctx, ev)

		case ev, ok := <-audioEvents:
			if !ok {
				audioEvents = nil
				continue
			}
			d.handleAudioEvent(ctx, ev)

		case req, ok := <-d.ipcRequests:
			if !ok {
				continue
			}
			d.handleIPC(ctx, req)
		}
	}
}

func (d *Daemon) handleBluetoothEvent(ctx context.Context, ev bluetooth.Event) {
	if ev.Connected {
		d.logEvent("bluetooth connected: %s (%s)", ev.DeviceName, ev.MACAddress)

		delay := time.Duration(d.cfg.Switching.SwitchDelayMs) * time.Millisecond
		time.Sleep(delay)

		d.refreshDevices(ctx)

		if d.cfg.Switching.OnConnect == "never" {
			return
		}

		d.mu.RLock()
		devices := d.devices
		priorities := d.cfg.Priority
		d.mu.RUnlock()

		var btDevice *audio.Device
		for i := range devices {
			if devices[i].Type == audio.DeviceTypeBluetooth &&
				(devices[i].MACAddress == ev.MACAddress || containsIgnoreCase(devices[i].Name, ev.DeviceName)) {
				btDevice = &devices[i]
				break
			}
		}

		if btDevice == nil {
			d.logEvent("bluetooth device not found as audio sink yet")
			return
		}

		if d.cfg.Switching.OnConnect == "priority" {
			currentDefault, _ := d.backend.GetDefaultSink(ctx)
			if currentDefault != nil {
				currentPrio := DevicePriority(*currentDefault, priorities)
				newPrio := DevicePriority(*btDevice, priorities)
				if newPrio >= currentPrio {
					d.logEvent("skipping switch: %s has lower priority", btDevice.Name)
					return
				}
			}
		}

		d.savePrevious(ctx)
		if err := d.backend.SetDefaultSink(ctx, btDevice.ID); err != nil {
			d.logEvent("switch failed: %v", err)
			return
		}
		d.logEvent("switched to %s", btDevice.Name)
		d.refreshDevices(ctx)

	} else {
		d.logEvent("bluetooth disconnected: %s (%s)", ev.DeviceName, ev.MACAddress)

		time.Sleep(300 * time.Millisecond)
		d.refreshDevices(ctx)

		currentDefault, _ := d.backend.GetDefaultSink(ctx)
		if currentDefault != nil && currentDefault.Available {
			return
		}

		d.mu.RLock()
		devices := d.devices
		priorities := d.cfg.Priority
		previousID := d.previousID
		d.mu.RUnlock()

		var target *audio.Device

		switch d.cfg.Switching.OnDisconnect {
		case "previous":
			for i := range devices {
				if devices[i].ID == previousID && devices[i].Available {
					target = &devices[i]
					break
				}
			}
		default:
			target = FindBestDevice(devices, priorities)
		}

		if target == nil {
			d.logEvent("no fallback device available")
			return
		}

		if err := d.backend.SetDefaultSink(ctx, target.ID); err != nil {
			d.logEvent("fallback switch failed: %v", err)
			return
		}
		d.logEvent("fallback to %s", target.Name)
		d.refreshDevices(ctx)
	}
}

func (d *Daemon) handleAudioEvent(ctx context.Context, ev audio.Event) {
	switch ev.Type {
	case audio.EventSinkAdded:
		d.logEvent("sink added: %s", ev.DeviceID)
		d.refreshDevices(ctx)
	case audio.EventSinkRemoved:
		d.logEvent("sink removed: %s", ev.DeviceID)
		d.refreshDevices(ctx)
	case audio.EventDefaultChanged:
		d.refreshDevices(ctx)
	}
}

func (d *Daemon) refreshDevices(ctx context.Context) {
	devices, err := d.backend.ListSinks(ctx)
	if err != nil {
		log.Printf("refresh devices: %v", err)
		return
	}
	d.mu.Lock()
	d.devices = devices
	d.mu.Unlock()
}

func (d *Daemon) savePrevious(ctx context.Context) {
	current, err := d.backend.GetDefaultSink(ctx)
	if err != nil {
		return
	}
	d.mu.Lock()
	d.previousID = current.ID
	d.mu.Unlock()
}

func (d *Daemon) logEvent(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Println(msg)
	d.mu.Lock()
	d.events = append(d.events, ipc.EventLog{Time: time.Now(), Message: msg})
	if len(d.events) > 200 {
		d.events = d.events[len(d.events)-200:]
	}
	d.mu.Unlock()
}

func (d *Daemon) GetDevices() []audio.Device {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]audio.Device, len(d.devices))
	copy(out, d.devices)
	return out
}

func (d *Daemon) GetEvents(limit int) []ipc.EventLog {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if limit <= 0 || limit > len(d.events) {
		limit = len(d.events)
	}
	out := make([]EventLog, limit)
	copy(out, d.events[len(d.events)-limit:])
	return out
}

func (d *Daemon) StartTime() time.Time {
	return d.startTime
}

func (d *Daemon) Config() config.Config {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg
}

func (d *Daemon) UpdatePriorities(priorities []config.PriorityEntry) {
	d.mu.Lock()
	d.cfg.Priority = priorities
	d.mu.Unlock()
}

func containsIgnoreCase(s, substr string) bool {
	if substr == "" {
		return false
	}
	return len(s) >= len(substr) &&
		(s == substr || len(s) > 0 && len(substr) > 0 &&
			containsFold(s, substr))
}

func containsFold(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if equalFold(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
