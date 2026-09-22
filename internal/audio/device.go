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
	// Available is false when this sink cannot play. That is an active port
	// marked not available, or a Barracuda X whose earcups are powered off.
	// An idle sink stays available, and so does a port reported as unknown.
	// The fallback skips a false value.
	Available  bool    `json:"available"`
	Volume     float64 `json:"volume"`
	Muted      bool    `json:"muted"`
	BusPath    string  `json:"bus_path,omitempty"`
	MACAddress string  `json:"mac_address,omitempty"`
	// VendorID and ProductID are the USB ids pactl reports, such as
	// 0x1532 and 0x054e for the Barracuda X receiver. Zero means the
	// server did not say.
	VendorID  uint16 `json:"vendor_id,omitempty"`
	ProductID uint16 `json:"product_id,omitempty"`
}
