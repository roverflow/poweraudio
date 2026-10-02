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
	// NotAudio is true only when BlueZ says the device offers no audio
	// output profile, such as a mouse or a keyboard. A device BlueZ could
	// not describe counts as audio, so an unknown headset still switches,
	// and so does an Event built without the field.
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

	// godbus drops signals rather than blocking when the channel it was given
	// is full, and this one carries every PropertiesChanged under /org/bluez,
	// most of which are not ours. A connect lost that way is a switch that
	// never happens, so there is room here for a burst.
	signals := make(chan *dbus.Signal, 256)
	m.conn.Signal(signals)

	// The consumer sleeps through switch_delay_ms while it waits for the sink
	// of a device that just connected, so events queue up behind it.
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

// deviceProps reads every Device1 property in one round trip. Asking for the
// alias, the profiles and the icon one at a time cost three. A device that has
// already gone away answers with an error, and an empty map then reads as an
// unnamed device of unknown kind.
func (m *Monitor) deviceProps(path string) map[string]dbus.Variant {
	var props map[string]dbus.Variant
	obj := m.conn.Object("org.bluez", dbus.ObjectPath(path))
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, "org.bluez.Device1").Store(&props); err != nil {
		return map[string]dbus.Variant{}
	}
	return props
}

// audioOutputUUIDs are the profiles a device advertises when it can play
// sound for us: A2DP sink, the headset and hands-free roles, and LE Audio's
// stream and capability services.
var audioOutputUUIDs = map[string]bool{
	"0000110b-0000-1000-8000-00805f9b34fb": true, // A2DP Audio Sink
	"0000110d-0000-1000-8000-00805f9b34fb": true, // A2DP
	"00001108-0000-1000-8000-00805f9b34fb": true, // Headset
	"00001131-0000-1000-8000-00805f9b34fb": true, // Headset HS
	"0000111e-0000-1000-8000-00805f9b34fb": true, // Handsfree
	"0000184e-0000-1000-8000-00805f9b34fb": true, // LE Audio stream control
	"00001850-0000-1000-8000-00805f9b34fb": true, // LE Audio published capabilities
}

// isAudio decides whether a device could carry our output. The profile list is
// the real answer. The icon covers a device whose profiles BlueZ has not
// resolved yet, and a device with neither is given the benefit of the doubt.
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
