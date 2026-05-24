package audio

import "context"

type EventType int

const (
	EventSinkAdded EventType = iota
	EventSinkRemoved
	EventDefaultChanged
)

type Event struct {
	Type     EventType
	DeviceID string
}

type Backend interface {
	Name() string
	ListSinks(ctx context.Context) ([]Device, error)
	GetDefaultSink(ctx context.Context) (*Device, error)
	SetDefaultSink(ctx context.Context, deviceID string) error
	SubscribeEvents(ctx context.Context) (<-chan Event, error)
}
