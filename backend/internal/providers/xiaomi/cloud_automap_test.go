package xiaomi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

const airConditionerCompanionSpec = `{"type":"urn:miot-spec-v2:device:air-conditioner:0000A004:lumi-mcn02:1","description":"Air Conditioner","services":[{"iid":2,"type":"urn:miot-spec-v2:service:air-conditioner:0000780F:lumi-mcn02:1","description":"Air Conditioner","properties":[{"iid":1,"type":"urn:miot-spec-v2:property:on:00000006:lumi-mcn02:1","description":"Switch Status","format":"bool","access":["read","write","notify"]},{"iid":2,"type":"urn:miot-spec-v2:property:mode:00000008:lumi-mcn02:1","description":"Mode","format":"uint8","access":["read","write","notify"],"value-list":[{"value":0,"description":"Auto"},{"value":1,"description":"Cool"},{"value":2,"description":"Dry"},{"value":3,"description":"Heat"},{"value":4,"description":"Fan"}]},{"iid":3,"type":"urn:miot-spec-v2:property:target-temperature:00000021:lumi-mcn02:1","description":"Target Temperature","format":"float","access":["read","write","notify"],"unit":"celsius","value-range":[16,30,1]}]}]}`

func generatedAirConditionerConfig() DeviceConfig {
	return DeviceConfig{
		DID: "124130332", ID: "xiaomi-miot-124130332", Name: "空调", Type: device.TypeAirConditioner, Model: "lumi.acpartner.mcn02",
		Properties: []PropertyMapping{
			{EndpointID: "main", CapabilityID: "air-conditioner", CapabilityType: "air-conditioner", PropertyID: "active", Name: "启用", ValueType: device.ValueTypeBool, SIID: 2, PIID: 1, Writable: true},
			{EndpointID: "main", CapabilityID: "air-conditioner", CapabilityType: "air-conditioner", PropertyID: "current-state", Name: "当前工作状态", ValueType: device.ValueTypeEnum, SIID: 2, PIID: 2, Enum: map[string]any{"off": 0, "idle": 1, "cooling": 2, "heating": 3, "drying": 4, "fan-only": 5}},
			{EndpointID: "main", CapabilityID: "air-conditioner", CapabilityType: "air-conditioner", PropertyID: "target-mode", Name: "运行模式", ValueType: device.ValueTypeEnum, SIID: 2, PIID: 3, Writable: true, Enum: map[string]any{"off": 0, "auto": 1, "cool": 2, "heat": 3, "dry": 4, "fan": 5}},
			{EndpointID: "main", CapabilityID: "temperature", CapabilityType: "temperature", PropertyID: "current-temperature", Name: "当前温度", ValueType: device.ValueTypeNumber, SIID: 2, PIID: 4},
			{EndpointID: "main", CapabilityID: "temperature", CapabilityType: "temperature", PropertyID: "target-temperature", Name: "目标温度", ValueType: device.ValueTypeNumber, SIID: 2, PIID: 5, Writable: true},
		},
	}
}

func TestCloudProviderReplacesGeneratedAirConditionerGuessWithMIoTSpec(t *testing.T) {
	document, err := decodeSpec([]byte(airConditionerCompanionSpec))
	if err != nil {
		t.Fatal(err)
	}
	configured, changed := autoMapCloudDevice(generatedAirConditionerConfig(), document)
	if !changed || len(configured.Properties) != 3 {
		t.Fatalf("changed=%v properties=%#v", changed, configured.Properties)
	}
	for index, propertyID := range []string{"active", "target-mode", "target-temperature"} {
		mapping := configured.Properties[index]
		if mapping.PropertyID != propertyID || mapping.SIID != 2 || mapping.PIID != index+1 {
			t.Fatalf("mapping %d = %#v", index, mapping)
		}
	}
	if mode := configured.Properties[1]; fmt.Sprint(mode.Enum["cool"]) != "1" {
		t.Fatalf("mode enum = %#v", mode.Enum)
	}
	if target := configured.Properties[2]; target.Min == nil || *target.Min != 16 || target.Max == nil || *target.Max != 30 || target.Step == nil || *target.Step != 1 {
		t.Fatalf("target temperature = %#v", target)
	}
}

func TestCloudProviderPublishesAirConditionerCompanionWithoutPollingGuessedProperties(t *testing.T) {
	configured := generatedAirConditionerConfig()
	config := CloudConfig{Region: "cn", UserID: "1", Ssecurity: "security", ServiceToken: "token", Devices: []DeviceConfig{configured}}
	config.applyDefaults()
	specType := "urn:miot-spec-v2:device:air-conditioner:0000A004:lumi-mcn02:1"
	cache := &memorySpecCache{document: []byte(airConditionerCompanionSpec), specType: specType, model: configured.Model, fetchedAt: time.Now().UTC()}
	fake := &fakeMIoTCloud{directory: []HubDevice{{DID: configured.DID, Name: configured.Name, Model: configured.Model, SpecType: specType}}, values: map[string]any{"2.1": true, "2.2": 1, "2.3": 24.0}}
	provider, err := newCloudProvider("xiaomi-miot-cloud-main", "Cloud", config, func() miotCloudClient { return fake }, NewSpecResolver(cache))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	items, err := provider.DiscoverDevices(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("devices=%#v err=%v", items, err)
	}
	item := items[0]
	if err := item.NormalizeModelParameters(); err != nil {
		t.Fatalf("normalized device: %v", err)
	}
	mode, _ := item.Property("main", "air-conditioner", "target-mode")
	target, _ := item.Property("main", "temperature", "target-temperature")
	if mode.Value.String == nil || *mode.Value.String != "cool" || target.Value.Number == nil || *target.Value.Number != 24 {
		t.Fatalf("mode=%#v target=%#v", mode, target)
	}
	if metrics := provider.ProviderMetrics(); metrics["errors"] != 0 {
		t.Fatalf("metrics = %#v", metrics)
	}
	provider.mu.RLock()
	runtimeMappings := append([]PropertyMapping(nil), provider.config.Devices[0].Properties...)
	provider.mu.RUnlock()
	if len(runtimeMappings) != 3 || runtimeMappings[1].PIID != 2 || runtimeMappings[2].PIID != 3 {
		t.Fatalf("runtime mappings = %#v", runtimeMappings)
	}
}

func TestCloudProviderKeepsCompleteSpecOutOfUnifiedDeviceSnapshot(t *testing.T) {
	const (
		model    = "vendor.switch.v1"
		specType = "urn:miot-spec-v2:device:switch:0000A003:vendor-v1:1"
		document = `{"type":"urn:miot-spec-v2:device:switch:0000A003:vendor-v1:1","description":"Switch","services":[{"iid":2,"type":"urn:miot-spec-v2:service:switch:0000780C:vendor-v1:1","description":"Switch","properties":[{"iid":1,"type":"urn:miot-spec-v2:property:on:00000006:vendor-v1:1","description":"Switch Status","format":"bool","access":["read","write","notify"]},{"iid":2,"type":"urn:miot-spec-v2:property:temperature:00000020:vendor-v1:1","description":"Temperature","format":"float","access":["read","notify"],"unit":"celsius","value-range":[-20,80,0.1]}]}]}`
	)
	config := cloudTestConfig()
	config.Devices[0].Model = model
	cache := &memorySpecCache{document: []byte(document), specType: specType, model: model, fetchedAt: time.Now().UTC()}
	fake := &fakeMIoTCloud{directory: []HubDevice{{DID: "123", Name: "云端开关", Model: model, SpecType: specType}}, values: map[string]any{"2.1": false, "2.2": 23.5}}
	provider, err := newCloudProvider("xiaomi-miot-cloud-main", "Cloud", config, func() miotCloudClient { return fake }, NewSpecResolver(cache))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	items, err := provider.DiscoverDevices(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("devices=%#v err=%v", items, err)
	}
	if _, exposed := items[0].Property("miot-2", "service-2", "property-2"); exposed {
		t.Fatalf("device detail snapshot exposed native cloud property: %#v", items[0])
	}
	catalog, err := provider.SourceCatalog(ctx)
	if err != nil || len(catalog) != 1 || !catalog[0].Catalog.Complete {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
	property, exists := catalog[0].Property("miot-2", "service-2", "property-2")
	status := catalog[0].Catalog.Values[providersdk.SourceValueKey("miot-2", "service-2", "property-2")]
	if !exists || property.Value.Number == nil || *property.Value.Number != 23.5 || !status.Known {
		t.Fatalf("native catalog property=%#v status=%#v", property, status)
	}
}

func TestCloudAirConditionerAutomapPreservesExplicitMappings(t *testing.T) {
	document, err := decodeSpec([]byte(airConditionerCompanionSpec))
	if err != nil {
		t.Fatal(err)
	}
	configured := generatedAirConditionerConfig()
	configured.Properties[1].PIID = 9
	result, changed := autoMapCloudDevice(configured, document)
	if changed || result.Properties[1].PIID != 9 {
		t.Fatalf("changed=%v result=%#v", changed, result.Properties)
	}
}
