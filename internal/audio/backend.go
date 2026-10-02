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
	// DefaultSinkName is the name of the current default sink, the same
	// string a Device carries as its ID. It costs one small call where
	// ListSinks costs two, which matters on every default-change event.
	DefaultSinkName(ctx context.Context) (string, error)
	SetDefaultSink(ctx context.Context, deviceID string) error
	SetVolume(ctx context.Context, deviceID string, percent int) error
	ToggleMute(ctx context.Context, deviceID string) error
	SubscribeEvents(ctx context.Context) (<-chan Event, error)
}
