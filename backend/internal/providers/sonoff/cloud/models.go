package cloud

import (
	"bytes"
	"encoding/json"
)

// CloudHome is a home/family returned by the eWeLink cloud API.
//
// eWeLink has used both familyid and homeid for the same identifier. The
// decoder fills both fields when either one is present.
type CloudHome struct {
	ID       string   `json:"id,omitempty"`
	HomeID   string   `json:"homeid,omitempty"`
	FamilyID string   `json:"familyid,omitempty"`
	Name     string   `json:"name,omitempty"`
	RoomIDs  []string `json:"roomids,omitempty"`
}

// Home is the concise integration-facing name for CloudHome.
type Home = CloudHome

// Device is a device returned by the eWeLink cloud API. Params is kept
// intentionally untyped because device capabilities differ between models.
// RawParams contains the exact JSON supplied by the service when params is
// present, so callers do not lose fields or number representations.
type Device struct {
	ID         string          `json:"id,omitempty"`
	DeviceID   string          `json:"deviceid,omitempty"`
	Name       string          `json:"name,omitempty"`
	Model      string          `json:"model,omitempty"`
	HomeID     string          `json:"homeid,omitempty"`
	HomeName   string          `json:"homename,omitempty"`
	FamilyID   string          `json:"familyid,omitempty"`
	RoomID     string          `json:"roomid,omitempty"`
	RoomName   string          `json:"roomname,omitempty"`
	RoomIDs    []string        `json:"roomids,omitempty"`
	Type       string          `json:"type,omitempty"`
	ProductKey string          `json:"productKey,omitempty"`
	UIID       int             `json:"uiid,omitempty"`
	Online     bool            `json:"online,omitempty"`
	DeviceKey  string          `json:"devicekey,omitempty"`
	Params     map[string]any  `json:"params,omitempty"`
	RawParams  json.RawMessage `json:"-"`
}

// CloudDevice is kept as the descriptive name used by the provider layer.
type CloudDevice = Device

// UnmarshalJSON accepts both a direct device object and the itemData wrapper
// used by the /v2/device/thing endpoint. DeviceKey is exposed because it is
// part of the provider contract, but it is never included in client errors.
func (d *Device) UnmarshalJSON(data []byte) error {
	type deviceAlias Device
	var decoded deviceAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	if itemData, ok := object["itemData"]; ok && len(bytes.TrimSpace(itemData)) > 0 && string(bytes.TrimSpace(itemData)) != "null" {
		var nested Device
		if err := json.Unmarshal(itemData, &nested); err != nil {
			return err
		}
		if nested.ID != "" {
			decoded.ID = nested.ID
		}
		if nested.DeviceID != "" {
			decoded.DeviceID = nested.DeviceID
		}
		if nested.Name != "" {
			decoded.Name = nested.Name
		}
		if nested.Model != "" {
			decoded.Model = nested.Model
		}
		if nested.HomeID != "" {
			decoded.HomeID = nested.HomeID
		}
		if nested.HomeName != "" {
			decoded.HomeName = nested.HomeName
		}
		if nested.FamilyID != "" {
			decoded.FamilyID = nested.FamilyID
		}
		if nested.RoomID != "" {
			decoded.RoomID = nested.RoomID
		}
		if nested.RoomName != "" {
			decoded.RoomName = nested.RoomName
		}
		if len(nested.RoomIDs) > 0 {
			decoded.RoomIDs = nested.RoomIDs
		}
		if nested.Type != "" {
			decoded.Type = nested.Type
		}
		if nested.ProductKey != "" {
			decoded.ProductKey = nested.ProductKey
		}
		if nested.UIID != 0 {
			decoded.UIID = nested.UIID
		}
		if nested.DeviceKey != "" {
			decoded.DeviceKey = nested.DeviceKey
		}
		if nested.Online {
			decoded.Online = true
		}
		if nested.Params != nil {
			decoded.Params = nested.Params
			decoded.RawParams = cloneRawMessage(nested.RawParams)
		}
	}

	if rawParams, ok := object["params"]; ok {
		decoded.RawParams = cloneRawMessage(rawParams)
		if len(bytes.TrimSpace(rawParams)) > 0 && string(bytes.TrimSpace(rawParams)) != "null" {
			var params map[string]any
			if err := json.Unmarshal(rawParams, &params); err != nil {
				return err
			}
			decoded.Params = params
		}
	}
	if decoded.DeviceKey == "" {
		for _, key := range []string{"devicekey", "apikey"} {
			if rawKey, ok := object[key]; ok {
				_ = json.Unmarshal(rawKey, &decoded.DeviceKey)
				if decoded.DeviceKey != "" {
					break
				}
			}
		}
	}
	if decoded.Model == "" {
		for _, key := range []string{"productModel", "model", "deviceType"} {
			if rawModel, ok := object[key]; ok {
				_ = json.Unmarshal(rawModel, &decoded.Model)
				if decoded.Model != "" {
					break
				}
			}
		}
	}

	*d = Device(decoded)
	d.normalizeIDs()
	return nil
}

func (d *CloudDevice) normalizeIDs() {
	if d.DeviceID == "" {
		d.DeviceID = d.ID
	}
	if d.ID == "" {
		d.ID = d.DeviceID
	}
	if d.HomeID == "" {
		d.HomeID = d.FamilyID
	}
	if d.FamilyID == "" {
		d.FamilyID = d.HomeID
	}
}

// CloudState is the state returned after a device command. Like device
// params, state is intentionally untyped and retains its raw JSON form.
type CloudState struct {
	DeviceID  string          `json:"deviceid,omitempty"`
	Params    map[string]any  `json:"params,omitempty"`
	RawParams json.RawMessage `json:"-"`
}

// Command is the cloud representation of a device state update.
type Command struct {
	DeviceID string         `json:"deviceid"`
	Params   map[string]any `json:"params"`
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}
