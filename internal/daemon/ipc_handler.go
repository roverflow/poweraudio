package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
)

// Handle answers one request. The server calls it on the connection's own
// goroutine, so a volume step no longer queues behind whatever the event loop
// is doing; the locks below are what keeps the state consistent instead.
// Subscribe is not handled here because it is a stream, not an answer.
func (d *Daemon) Handle(ctx context.Context, req ipc.Request) ipc.Response {
	switch req.Method {
	case ipc.MethodSnapshot:
		return ipc.SuccessResponse(d.Snapshot())

	case ipc.MethodSetDefault:
		var params ipc.SetDefaultParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return ipc.ErrorResponse("invalid params: " + err.Error())
		}
		if err := d.SetDefault(ctx, params.DeviceID); err != nil {
			d.errorf("manual switch failed: %v", err)
			return ipc.ErrorResponse(err.Error())
		}
		d.infof("manual switch to %s", d.deviceName(params.DeviceID))
		return ipc.SuccessResponse(nil)

	case ipc.MethodSetVolume:
		var params ipc.SetVolumeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return ipc.ErrorResponse("invalid params: " + err.Error())
		}
		if err := d.backend.SetVolume(ctx, params.DeviceID, params.Percent); err != nil {
			return ipc.ErrorResponse(err.Error())
		}
		d.refreshDevices(ctx)
		return ipc.SuccessResponse(nil)

	case ipc.MethodToggleMute:
		var params ipc.ToggleMuteParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return ipc.ErrorResponse("invalid params: " + err.Error())
		}
		if err := d.backend.ToggleMute(ctx, params.DeviceID); err != nil {
			return ipc.ErrorResponse(err.Error())
		}
		d.refreshDevices(ctx)
		return ipc.SuccessResponse(nil)

	case ipc.MethodUpdatePriorities:
		var entries []config.PriorityEntry
		if err := json.Unmarshal(req.Params, &entries); err != nil {
			return ipc.ErrorResponse("invalid params: " + err.Error())
		}
		cfg := d.updatePriorities(entries)
		d.infof("priorities updated (%d entries)", len(entries))
		return d.saveResponse(cfg)

	case ipc.MethodUpdateSwitching:
		var params ipc.UpdateSwitchingParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return ipc.ErrorResponse("invalid params: " + err.Error())
		}
		cfg := d.updateSwitching(params.OnConnect, params.OnDisconnect)
		d.infof("switching config updated: on_connect=%s on_disconnect=%s",
			params.OnConnect, params.OnDisconnect)
		return d.saveResponse(cfg)

	case ipc.MethodReloadConfig:
		path := config.ResolvePath(d.configPath)
		cfg, err := config.Load(d.configPath)
		if err != nil {
			d.errorf("reloading config from %s: %v", path, err)
			return ipc.ErrorResponse(err.Error())
		}
		d.applyConfig(cfg)
		d.infof("config reloaded from %s", path)
		return ipc.SuccessResponse(nil)

	case ipc.MethodSubscribe:
		return ipc.ErrorResponse("subscribe is a streaming method")

	default:
		return ipc.ErrorResponse("unknown method: " + req.Method)
	}
}

// Snapshot is the daemon's visible state at one moment: the sink list, who it
// is and where it reads its config, every retained event oldest first, and the
// running config.
func (d *Daemon) Snapshot() ipc.Snapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	devices := make([]audio.Device, len(d.devices))
	copy(devices, d.devices)
	events := make([]ipc.EventLog, len(d.events))
	copy(events, d.events)

	return ipc.Snapshot{
		Devices: devices,
		Status: ipc.StatusData{
			Backend:    d.backend.Name(),
			ConfigPath: config.ResolvePath(d.configPath),
			StartedAt:  d.startTime,
		},
		Events: events,
		Config: copyConfig(d.cfg),
	}
}

// saveResponse reports a failed write as a failure. The change is already
// live either way, but saying OK while the file went untouched meant the UI
// showed "saved" over a config that would be gone at the next restart.
func (d *Daemon) saveResponse(cfg config.Config) ipc.Response {
	if err := d.saveConfig(cfg); err != nil {
		msg := fmt.Sprintf("saving %s: %v", config.ResolvePath(d.configPath), err)
		d.errorf("%s", msg)
		return ipc.ErrorResponse(msg)
	}
	return ipc.SuccessResponse(nil)
}

// saveConfig writes the config file and remembers the mtime it left, so the
// watcher in runEvents can tell this write apart from an edit by hand.
func (d *Daemon) saveConfig(cfg config.Config) error {
	d.saveMu.Lock()
	defer d.saveMu.Unlock()

	if err := config.Save(d.configPath, cfg); err != nil {
		return err
	}
	if info, err := os.Stat(config.ResolvePath(d.configPath)); err == nil {
		d.ownSaveMod = info.ModTime()
	} else {
		d.ownSaveMod = time.Time{}
	}
	return nil
}

func (d *Daemon) updatePriorities(entries []config.PriorityEntry) config.Config {
	d.mu.Lock()
	d.cfg.Priority = entries
	cfg := copyConfig(d.cfg)
	d.mu.Unlock()
	d.changed()
	return cfg
}

func (d *Daemon) updateSwitching(onConnect, onDisconnect string) config.Config {
	d.mu.Lock()
	d.cfg.Switching.OnConnect = onConnect
	d.cfg.Switching.OnDisconnect = onDisconnect
	cfg := copyConfig(d.cfg)
	d.mu.Unlock()
	d.changed()
	return cfg
}
