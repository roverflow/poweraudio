package audio

type DeviceType int

const (
	DeviceTypeSpeaker DeviceType = iota
	DeviceTypeHeadphone
	DeviceTypeBluetooth
	DeviceTypeHDMI
	DeviceTypeUSB
	DeviceTypeUnknown
)

func (d DeviceType) String() string {
	switch d {
	case DeviceTypeSpeaker:
		return "Speaker"
	case DeviceTypeHeadphone:
		return "Headphone"
	case DeviceTypeBluetooth:
		return "Bluetooth"
	case DeviceTypeHDMI:
		return "HDMI"
	case DeviceTypeUSB:
		return "USB"
	default:
		return "Unknown"
	}
}

type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Description is the technical sink name, such as
	// "alsa_output.usb-1532_Razer_Barracuda_X_R002000000-01.analog-stereo",
	// where Name is the description a person reads. Both are kept because a
	// priority entry matches against either, and the detail panel shows the
	// technical one when two devices read alike.
	Description string     `json:"description"`
	Type        DeviceType `json:"type"`
	IsDefault   bool       `json:"is_default"`
	Available   bool       `json:"available"`
	Volume      float64    `json:"volume"`
	Muted       bool       `json:"muted"`
	BusPath     string     `json:"bus_path,omitempty"`
	MACAddress  string     `json:"mac_address,omitempty"`
}
