// Package ipc is the wire protocol between the daemon and its clients, and
// the client that speaks it. Requests are newline-delimited JSON over a Unix
// socket. Every method is one request and one response, except subscribe,
// which keeps the connection open and streams a response per change.
package ipc

import (
	"encoding/json"
	"time"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
)

const (
	// MethodSnapshot returns everything a client needs to draw a screen in
	// one round trip: devices, daemon status, the event log and the config.
	MethodSnapshot = "snapshot"

	// MethodSubscribe sends a Snapshot immediately and then another one each
	// time anything in it changes, until the client closes the connection.
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

// Level is how serious a logged event is. The daemon decides this when it
// writes the line, so clients colour and filter without parsing sentences.
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
	Backend    string    `json:"backend"`
	ConfigPath string    `json:"config_path"`
	StartedAt  time.Time `json:"started_at"`
}

// Snapshot is the daemon's whole visible state at one moment. Events are
// oldest first and bounded by the daemon's retention.
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
