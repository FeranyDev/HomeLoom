package device

// NamePreference is a HomeLoom-owned display-name override for one unified
// device. It deliberately belongs to the unified device instead of a Provider
// configuration, so every Provider and every target sees the same name.
type NamePreference struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
}
