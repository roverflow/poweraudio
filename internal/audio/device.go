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
	// Description is the sink name. Priority entries match it as well as Name.
	Description string     `json:"description"`
	Type        DeviceType `json:"type"`
	IsDefault   bool       `json:"is_default"`
	// Available is false when the active port is empty or the Barracuda X
	// earcups are off.
	Available  bool    `json:"available"`
	Volume     float64 `json:"volume"`
	Muted      bool    `json:"muted"`
	BusPath    string  `json:"bus_path,omitempty"`
	MACAddress string  `json:"mac_address,omitempty"`
	// VendorID and ProductID are zero when the server publishes no USB id.
	VendorID  uint16 `json:"vendor_id,omitempty"`
	ProductID uint16 `json:"product_id,omitempty"`
	// Virtual marks a sink with no local hardware, such as a null sink, a
	// filter chain or an AirPlay speaker. The fallback skips it unless ranked.
	Virtual bool `json:"virtual,omitempty"`
}

// PlaceholderID is the "Dummy Output" sink. PipeWire refuses to make it the
// default, so it is never a target.
const PlaceholderID = "auto_null"

// IsPlaceholder reports whether this is the "Dummy Output" sink.
func (d Device) IsPlaceholder() bool {
	return d.ID == PlaceholderID
}

// Usable reports whether the sink can play and is not the placeholder.
func (d Device) Usable() bool {
	return d.Available && !d.IsPlaceholder()
}
