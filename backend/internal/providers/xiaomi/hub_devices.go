package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

// HubDevice is the identity metadata returned by the central gateway before a
// device is mapped into HomeLoom's unified model.
type HubDevice struct {
	DID      string `json:"did"`
	Name     string `json:"name"`
	Model    string `json:"model,omitempty"`
	RoomID   string `json:"roomId,omitempty"`
	RoomName string `json:"roomName,omitempty"`
	SpecType string `json:"specType,omitempty"`
	Online   *bool  `json:"online,omitempty"`
}

// DiscoverHubDevices opens a short-lived mTLS/MQTT connection and asks the
// selected central gateway for its device directory. In-progress mappings are
// deliberately ignored so users can fetch the directory before creating them.
func DiscoverHubDevices(ctx context.Context, item providerconfig.Config) ([]HubDevice, error) {
	var rawConfig map[string]any
	if err := json.Unmarshal(item.Config, &rawConfig); err != nil || rawConfig == nil {
		return nil, errors.New("Xiaomi provider config must be a JSON object")
	}
	rawConfig["devices"] = []any{}
	encoded, err := json.Marshal(rawConfig)
	if err != nil {
		return nil, err
	}
	item.Config = encoded
	config, brokerURL, tlsConfig, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	client := newMIPSClient(config, brokerURL, tlsConfig)
	client.SetIncomingHandler(func(hubIncoming) {})
	lifecycle, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Connect(lifecycle, ctx); err != nil {
		return nil, err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = client.Close(closeCtx)
	}()
	raw, err := client.DeviceList(ctx)
	if err != nil {
		return nil, err
	}
	return parseHubDeviceList(raw)
}

func parseHubDeviceList(raw json.RawMessage) ([]HubDevice, error) {
	if err := responseOK(raw); err != nil {
		return nil, err
	}
	var envelope any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, err
	}
	items := make(map[string]HubDevice)
	// getDevList is already a dedicated device-directory response. Treat every
	// result collection as directory data instead of requiring a particular
	// result.list envelope: firmware variants return result arrays, dev_list,
	// data objects, and occasionally JSON encoded inside a string. A top-level
	// array is also a directory; a top-level object can still be an envelope.
	_, topLevelArray := envelope.([]any)
	collectHubDevices(envelope, items, topLevelArray)
	result := make([]HubDevice, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RoomName != result[j].RoomName {
			return result[i].RoomName < result[j].RoomName
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].DID < result[j].DID
	})
	return result, nil
}

func collectHubDevices(value any, output map[string]HubDevice, deviceList bool) {
	switch current := value.(type) {
	case string:
		trimmed := strings.TrimSpace(current)
		if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
			return
		}
		var nested any
		decoder := json.NewDecoder(strings.NewReader(trimmed))
		decoder.UseNumber()
		if decoder.Decode(&nested) == nil {
			collectHubDevices(nested, output, deviceList)
		}
	case []any:
		for _, entry := range current {
			collectHubDevices(entry, output, deviceList)
		}
	case map[string]any:
		did := firstString(current, "did", "deviceId", "device_id", "miotDid", "miot_did")
		if did != "" && (deviceList || hasAnyKey(current, "name", "deviceName", "model", "specType", "spec_type", "urn")) {
			name := firstString(current, "name", "deviceName", "device_name", "devName", "dev_name", "displayName")
			model := firstString(current, "model", "modelName", "model_name", "productModel")
			if name == "" {
				name = model
			}
			if name == "" {
				name = did
			}
			item := HubDevice{DID: did, Name: name, Model: model, RoomID: firstString(current, "roomId", "room_id", "roomDid", "room_did"), RoomName: firstString(current, "roomName", "room_name", "room", "room_name_i18n"), SpecType: firstString(current, "specType", "spec_type", "urn", "type")}
			if online, ok := boolValue(firstValue(current, "online", "isOnline", "is_online")); ok {
				item.Online = &online
			}
			output[did] = item
			return
		}
		for key, child := range current {
			// A few firmware versions use the DID as the object key instead of
			// repeating it inside each directory entry.
			if deviceList {
				if childObject, ok := child.(map[string]any); ok && firstString(childObject, "did", "deviceId", "device_id", "miotDid", "miot_did") == "" && hasAnyKey(childObject, "name", "deviceName", "device_name", "devName", "model", "type") {
					withDID := make(map[string]any, len(childObject)+1)
					for childKey, childValue := range childObject {
						withDID[childKey] = childValue
					}
					withDID["did"] = key
					collectHubDevices(withDID, output, true)
					continue
				}
			}
			nestedList := deviceList || key == "result" || key == "data" || key == "list" || key == "devices" || key == "devList" || key == "deviceList" || key == "dev_list" || key == "device_list"
			collectHubDevices(child, output, nestedList)
		}
	}
}

func firstValue(input map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := input[key]; ok {
			return value
		}
	}
	return nil
}

func boolValue(value any) (bool, bool) {
	switch current := value.(type) {
	case bool:
		return current, true
	case json.Number:
		if current.String() == "1" {
			return true, true
		}
		if current.String() == "0" {
			return false, true
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(current)) {
		case "1", "true", "online":
			return true, true
		case "0", "false", "offline":
			return false, true
		}
	}
	return false, false
}

func firstString(input map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		if value, ok := input[key].(json.Number); ok && value.String() != "" {
			return value.String()
		}
	}
	return ""
}

func hasAnyKey(input map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := input[key]; ok {
			return true
		}
	}
	return false
}
