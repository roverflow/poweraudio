package audio

import "context"

type EventType int

const (
	EventSinkAdded EventType = iota
	EventSinkRemoved
	EventDefaultChanged
	EventSinkChanged
)

type Event struct {
	Type     EventType
	DeviceID string
}

type Backend interface {
	Name() string
	ListSinks(ctx context.Context) ([]Device, error)
	// DefaultSinkName costs one pactl call where ListSinks costs two.
	DefaultSinkName(ctx context.Context) (string, error)
	SetDefaultSink(ctx context.Context, deviceID string) error
	SetVolume(ctx context.Context, deviceID string, percent int) error
	ToggleMute(ctx context.Context, deviceID string) error
	SubscribeEvents(ctx context.Context) (<-chan Event, error)
}
