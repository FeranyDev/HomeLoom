package mcp

import (
	"strings"
	"testing"
)

func TestDeviceConfigValidationAndDefaults(t *testing.T) {
	config := DeviceConfig{DeviceID: "desk-lamp", Enabled: true}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if normalized := config.Normalize(); normalized.DefaultAccess != AccessHidden {
		t.Fatalf("default access = %q", normalized.DefaultAccess)
	}
	if err := (DeviceConfig{DeviceID: "not valid", DefaultAccess: AccessRead}).Validate(); err == nil {
		t.Fatal("invalid device id was accepted")
	}
	if err := (DeviceConfig{DeviceID: "desk-lamp", DefaultAccess: Access("write")}).Validate(); err == nil {
		t.Fatal("invalid access was accepted")
	}
	if err := (DeviceConfig{DeviceID: "desk-lamp", UsageNote: strings.Repeat("a", MaxUsageNoteRunes+1)}).Validate(); err == nil {
		t.Fatal("oversize note was accepted")
	}
}

func TestEffectivePropertyConfigCannotEscapeDisabledDevice(t *testing.T) {
	path := PropertyPath{DeviceID: "desk-lamp", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}
	effective := Effective(DeviceConfig{DeviceID: "desk-lamp", Enabled: false, DefaultAccess: AccessConfirm}, PropertyConfig{PropertyPath: path, Access: AccessConfirm})
	if effective.EffectiveAccess != AccessHidden {
		t.Fatalf("disabled effective access = %q", effective.EffectiveAccess)
	}
	effective = Effective(DeviceConfig{DeviceID: "desk-lamp", Enabled: true, DefaultAccess: AccessRead}, PropertyConfig{PropertyPath: path, Access: AccessInherit})
	if effective.EffectiveAccess != AccessRead {
		t.Fatalf("inherited effective access = %q", effective.EffectiveAccess)
	}
}
