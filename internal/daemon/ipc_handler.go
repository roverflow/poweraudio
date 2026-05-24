package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
)

type Request = ipc.Request
type Response = ipc.Response

func (d *Daemon) handleIPC(ctx context.Context, req IPCRequest) {
	var resp Response

	switch req.Request.Method {
	case ipc.MethodListDevices:
		resp = ipc.SuccessResponse(d.GetDevices())

	case ipc.MethodGetDefault:
		dev, err := d.backend.GetDefaultSink(ctx)
		if err != nil {
			resp = ipc.ErrorResponse(err.Error())
		} else {
			resp = ipc.SuccessResponse(dev)
		}

	case ipc.MethodSetDefault:
		var params ipc.SetDefaultParams
		if err := json.Unmarshal(req.Request.Params, &params); err != nil {
			resp = ipc.ErrorResponse("invalid params: " + err.Error())
		} else if err := d.backend.SetDefaultSink(ctx, params.DeviceID); err != nil {
			resp = ipc.ErrorResponse(err.Error())
		} else {
			d.logEvent("manual switch to device %s", params.DeviceID)
			d.refreshDevices(ctx)
			resp = ipc.SuccessResponse(nil)
		}

	case ipc.MethodGetStatus:
		resp = ipc.SuccessResponse(ipc.StatusData{
			Backend:   d.backend.Name(),
			Socket:    d.cfg.Daemon.SocketPath,
			UptimeSec: int(time.Since(d.startTime).Seconds()),
		})

	case ipc.MethodGetEvents:
		var params ipc.GetEventsParams
		limit := 50
		if err := json.Unmarshal(req.Request.Params, &params); err == nil && params.Limit > 0 {
			limit = params.Limit
		}
		resp = ipc.SuccessResponse(d.GetEvents(limit))

	case ipc.MethodGetConfig:
		resp = ipc.SuccessResponse(d.Config())

	case ipc.MethodUpdatePriorities:
		var priorities []config.PriorityEntry
		if err := json.Unmarshal(req.Request.Params, &priorities); err != nil {
			resp = ipc.ErrorResponse("invalid params: " + err.Error())
		} else {
			d.UpdatePriorities(priorities)
			cfg := d.Config()
			if err := config.Save("", cfg); err != nil {
				d.logEvent("failed to save config: %v", err)
			}
			d.logEvent("priorities updated (%d entries)", len(priorities))
			resp = ipc.SuccessResponse(nil)
		}

	case ipc.MethodUpdateSwitching:
		var params ipc.UpdateSwitchingParams
		if err := json.Unmarshal(req.Request.Params, &params); err != nil {
			resp = ipc.ErrorResponse("invalid params: " + err.Error())
		} else {
			d.mu.Lock()
			d.cfg.Switching.OnConnect = params.OnConnect
			d.cfg.Switching.OnDisconnect = params.OnDisconnect
			cfg := d.cfg
			d.mu.Unlock()
			if err := config.Save("", cfg); err != nil {
				d.logEvent("failed to save config: %v", err)
			}
			d.logEvent("switching config updated: on_connect=%s on_disconnect=%s",
				params.OnConnect, params.OnDisconnect)
			resp = ipc.SuccessResponse(nil)
		}

	case ipc.MethodReloadConfig:
		cfg, err := config.Load("")
		if err != nil {
			resp = ipc.ErrorResponse(err.Error())
		} else {
			d.mu.Lock()
			d.cfg = cfg
			d.mu.Unlock()
			d.logEvent("config reloaded")
			resp = ipc.SuccessResponse(nil)
		}

	default:
		resp = ipc.ErrorResponse("unknown method: " + req.Request.Method)
	}

	req.Response <- resp
}
