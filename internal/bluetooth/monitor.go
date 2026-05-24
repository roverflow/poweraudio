package bluetooth

import (
	"context"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
)

type Event struct {
	MACAddress string
	DeviceName string
	Connected  bool
	ObjectPath string
}

type Monitor struct {
	conn *dbus.Conn
}

func NewMonitor() (*Monitor, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connecting to system D-Bus: %w", err)
	}
	return &Monitor{conn: conn}, nil
}

func (m *Monitor) Close() {
	if m.conn != nil {
		m.conn.Close()
	}
}

func (m *Monitor) Subscribe(ctx context.Context) (<-chan Event, error) {
	if err := m.conn.AddMatchSignal(
		dbus.WithMatchObjectPath("/"),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchSender("org.bluez"),
		dbus.WithMatchPathNamespace("/org/bluez"),
	); err != nil {
		return nil, fmt.Errorf("adding D-Bus match: %w", err)
	}

	signals := make(chan *dbus.Signal, 32)
	m.conn.Signal(signals)

	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				if ev, ok := m.parseSignal(sig); ok {
					select {
					case ch <- ev:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return ch, nil
}

func (m *Monitor) parseSignal(sig *dbus.Signal) (Event, bool) {
	if len(sig.Body) < 2 {
		return Event{}, false
	}

	iface, ok := sig.Body[0].(string)
	if !ok || iface != "org.bluez.Device1" {
		return Event{}, false
	}

	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return Event{}, false
	}

	connVariant, ok := changed["Connected"]
	if !ok {
		return Event{}, false
	}
	connected, ok := connVariant.Value().(bool)
	if !ok {
		return Event{}, false
	}

	path := string(sig.Path)
	mac := macFromPath(path)
	name := m.getDeviceName(path)

	return Event{
		MACAddress: mac,
		DeviceName: name,
		Connected:  connected,
		ObjectPath: path,
	}, true
}

func (m *Monitor) getDeviceName(path string) string {
	obj := m.conn.Object("org.bluez", dbus.ObjectPath(path))
	variant, err := obj.GetProperty("org.bluez.Device1.Alias")
	if err != nil {
		return ""
	}
	name, _ := variant.Value().(string)
	return name
}

func macFromPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if !strings.HasPrefix(last, "dev_") {
		return ""
	}
	mac := strings.TrimPrefix(last, "dev_")
	return strings.ReplaceAll(mac, "_", ":")
}
