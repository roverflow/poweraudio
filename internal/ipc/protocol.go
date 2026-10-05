// Package ipc is the daemon's newline-delimited JSON protocol over a Unix
// socket, and the client that speaks it.
package ipc

import (
	"encoding/json"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/probe"
)

const (
	// MethodSnapshot returns devices, status, the event log and the config.
	MethodSnapshot = "snapshot"

	// MethodSubscribe streams a Snapshot now and after every change.
	MethodSubscribe = "subscribe"

	MethodSetDefault       = "set_default"
	MethodSetVolume        = "set_volume"
	MethodToggleMute       = "toggle_mute"
	MethodUpdatePriorities = "update_priorities"
	MethodUpdateSwitching  = "update_switching"
	MethodReloadConfig     = "reload_config"
)

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Level is how serious a logged event is.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

type EventLog struct {
	Time    time.Time `json:"time"`
	Level   Level     `json:"level"`
	Message string    `json:"message"`
}

type StatusData struct {
	// Version is the daemon's release. Older daemons leave it empty.
	Version    string    `json:"version,omitempty"`
	Backend    string    `json:"backend"`
	ConfigPath string    `json:"config_path"`
	StartedAt  time.Time `json:"started_at"`
	// Switching names the switching engine, such as "pactl".
	Switching string `json:"switching,omitempty"`
	// Audio is the zero value until the first probe finishes.
	Audio probe.Report `json:"audio"`
}

// Snapshot is the daemon's visible state. Events are oldest first.
type Snapshot struct {
	Devices []audio.Device `json:"devices"`
	Status  StatusData     `json:"status"`
	Events  []EventLog     `json:"events"`
	Config  config.Config  `json:"config"`
}

// Default returns the current default device, or nil when there is none.
func (s *Snapshot) Default() *audio.Device {
	for i := range s.Devices {
		if s.Devices[i].IsDefault {
			return &s.Devices[i]
		}
	}
	return nil
}

type SetDefaultParams struct {
	DeviceID string `json:"device_id"`
	// Notify asks for a desktop notification, for hotkeys with no terminal.
	Notify bool `json:"notify,omitempty"`
}

type SetVolumeParams struct {
	DeviceID string `json:"device_id"`
	Percent  int    `json:"percent"`
}

type ToggleMuteParams struct {
	DeviceID string `json:"device_id"`
}

type UpdateSwitchingParams struct {
	OnConnect    string `json:"on_connect"`
	OnDisconnect string `json:"on_disconnect"`
}

func SuccessResponse(data any) Response {
	raw, _ := json.Marshal(data)
	return Response{OK: true, Data: raw}
}

func ErrorResponse(msg string) Response {
	return Response{OK: false, Error: msg}
}
