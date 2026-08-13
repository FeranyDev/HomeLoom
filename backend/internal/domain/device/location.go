package device

import "errors"

var (
	ErrLocationNotFound = errors.New("location not found")
	ErrLocationInUse    = errors.New("location is in use")
	ErrLocationConflict = errors.New("location name already exists")
)

// LocationPreference is a HomeLoom-owned location override for one unified
// device. Provider-reported locations remain on the Device's source fields and
// are restored whenever this preference is removed.
type LocationPreference struct {
	DeviceID string `json:"deviceId"`
	HomeID   string `json:"homeId"`
	HomeName string `json:"homeName"`
	RoomID   string `json:"roomId,omitempty"`
	RoomName string `json:"roomName,omitempty"`
}

// LocationHome and LocationRoom form the HomeLoom-owned location directory.
// Unified devices reference these stable IDs instead of accepting arbitrary
// free-form location text at assignment time.
type LocationHome struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Rooms []LocationRoom `json:"rooms"`
}

type LocationRoom struct {
	ID     string `json:"id"`
	HomeID string `json:"homeId"`
	Name   string `json:"name"`
}
