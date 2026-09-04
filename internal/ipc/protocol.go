package ipc

import (
	"encoding/json"
	"time"

	"github.com/roverflow/poweraudio/internal/config"
)

const (
	MethodListDevices      = "list_devices"
	MethodGetDefault       = "get_default"
	MethodSetDefault       = "set_default"
	MethodGetStatus        = "get_status"
	MethodGetEvents        = "get_events"
	MethodGetConfig        = "get_config"
	MethodUpdatePriorities = "update_priorities"
	MethodSetVolume        = "set_volume"
	MethodToggleMute       = "toggle_mute"
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

type SetDefaultParams struct {
	DeviceID string `json:"device_id"`
}

type GetEventsParams struct {
	Limit int `json:"limit"`
}

type StatusData struct {
	Backend   string `json:"backend"`
	Socket    string `json:"socket"`
	UptimeSec int    `json:"uptime_sec"`
}

type EventLog struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

type PriorityEntry = config.PriorityEntry

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
