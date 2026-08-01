package target

import "testing"

func TestNormalizeProtocolConfigMigratesLegacyHomeKitFields(t *testing.T) {
	legacy := Config{
		ID: "apple-main", Type: "apple-hap", Address: ":51826",
		Pin: "12345678", SetupID: "HLM1", StorePath: "data/hap/apple-main",
	}
	normalized := legacy.NormalizeProtocolConfig()
	if normalized.HomeKitConfig == nil ||
		normalized.HomeKitConfig.Address != legacy.Address ||
		normalized.HomeKitConfig.Pin != legacy.Pin ||
		normalized.HomeKitConfig.SetupID != legacy.SetupID ||
		normalized.HomeKitConfig.StorePath != legacy.StorePath {
		t.Fatalf("normalized HomeKit config = %#v", normalized)
	}
	if normalized.MatterConfig != nil {
		t.Fatalf("HomeKit target retained Matter config = %#v", normalized.MatterConfig)
	}
}

func TestNormalizeProtocolConfigPreventsMatterFromUsingHomeKitFields(t *testing.T) {
	discriminator := uint16(1)
	normalized := (Config{
		ID: "matter-main", Type: "matter",
		Address: ":51826", Pin: "12345678", SetupID: "HLM1", StorePath: "data/hap/apple-main",
		HomeKitConfig: &HomeKitConfig{Address: ":51827"},
		MatterConfig:  &MatterConfig{Discriminator: &discriminator, Passcode: "20202021"},
	}).NormalizeProtocolConfig()
	if normalized.HomeKitConfig != nil || normalized.Address != "" || normalized.Pin != "" ||
		normalized.SetupID != "" || normalized.StorePath != "" {
		t.Fatalf("Matter target retained HomeKit fields = %#v", normalized)
	}
	if normalized.MatterConfig == nil || normalized.MatterConfig.Passcode != "20202021" {
		t.Fatalf("Matter config was lost = %#v", normalized.MatterConfig)
	}
}

func TestMatterCameraDescriptorAndNormalization(t *testing.T) {
	descriptor, found := DescriptorForType("matter-camera")
	if !found || descriptor.ConsumerID != "matter-camera" || descriptor.DefaultIDPrefix != "camera-matter" {
		t.Fatalf("Matter Camera descriptor = %#v, %v", descriptor, found)
	}
	normalized := (Config{
		Type: "matter-camera", Address: ":51826", Pin: "12345678",
		HomeKitConfig: &HomeKitConfig{Address: ":51826"}, MatterConfig: &MatterConfig{},
	}).NormalizeProtocolConfig()
	if !IsMatterType(normalized.Type) || normalized.HomeKitConfig != nil || normalized.Address != "" || normalized.MatterConfig == nil {
		t.Fatalf("normalized Matter Camera config = %#v", normalized)
	}
}
