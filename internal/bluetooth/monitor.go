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
	// NotAudio is true only when BlueZ says the device has no audio
	// profile, so an unknown device still switches.
	NotAudio bool
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
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchPathNamespace("/org/bluez"),
	); err != nil {
		return nil, fmt.Errorf("adding D-Bus match: %w", err)
	}

	// godbus drops signals when this channel is full, and a dropped connect
	// is a missed switch.
	signals := make(chan *dbus.Signal, 256)
	m.conn.Signal(signals)

	// The consumer sleeps through switch_delay_ms, so events queue here.
	ch := make(chan Event, 64)
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
	props := m.deviceProps(path)
	name, _ := props["Alias"].Value().(string)
	uuids, _ := props["UUIDs"].Value().([]string)
	icon, _ := props["Icon"].Value().(string)

	return Event{
		MACAddress: macFromPath(path),
		DeviceName: name,
		Connected:  connected,
		ObjectPath: path,
		NotAudio:   !isAudio(uuids, icon),
	}, true
}

// deviceProps returns an empty map when the device has already gone.
func (m *Monitor) deviceProps(path string) map[string]dbus.Variant {
	var props map[string]dbus.Variant
	obj := m.conn.Object("org.bluez", dbus.ObjectPath(path))
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, "org.bluez.Device1").Store(&props); err != nil {
		return map[string]dbus.Variant{}
	}
	return props
}

var audioOutputUUIDs = map[string]bool{
	"0000110b-0000-1000-8000-00805f9b34fb": true, // A2DP Audio Sink
	"0000110d-0000-1000-8000-00805f9b34fb": true, // A2DP
	"00001108-0000-1000-8000-00805f9b34fb": true, // Headset
	"00001131-0000-1000-8000-00805f9b34fb": true, // Headset HS
	"0000111e-0000-1000-8000-00805f9b34fb": true, // Handsfree
	"0000184e-0000-1000-8000-00805f9b34fb": true, // LE Audio stream control
	"00001850-0000-1000-8000-00805f9b34fb": true, // LE Audio published capabilities
}

// isAudio checks profiles, then the icon for a device BlueZ has not resolved
// yet. A device with neither counts as audio.
func isAudio(uuids []string, icon string) bool {
	for _, u := range uuids {
		if audioOutputUUIDs[strings.ToLower(u)] {
			return true
		}
	}
	if strings.HasPrefix(icon, "audio-") {
		return true
	}
	return len(uuids) == 0 && icon == ""
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
