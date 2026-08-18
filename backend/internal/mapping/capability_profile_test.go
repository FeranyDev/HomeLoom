package mapping

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func TestAutoCapabilityProfileBuildsStableReversibleUnitRules(t *testing.T) {
	profile, ok := AutoCapabilityProfile(device.ValueTypeNumber, device.ValueTypeInt, "kelvin", "mired")
	if !ok {
		t.Fatal("kelvin to mired profile was not generated")
	}
	if profile.ID != "builtin-capability-kelvin-to-mired-number-to-int" || profile.Kind != KindCapability {
		t.Fatalf("profile = %#v", profile)
	}
	if len(profile.Transforms) != 2 || profile.Transforms[0].Type != TransformUnit || profile.Transforms[1].Type != TransformRound {
		t.Fatalf("transforms = %#v", profile.Transforms)
	}
	forward, err := Preview(PreviewRequest{Profile: profile, Direction: DirectionForward, Value: valuePointer(device.NumberValue(4000))})
	if err != nil || forward.Value.Int == nil || *forward.Value.Int != 250 {
		t.Fatalf("forward = %#v, error = %v", forward, err)
	}
	reverse, err := Preview(PreviewRequest{Profile: profile, Direction: DirectionReverse, Value: valuePointer(device.IntValue(250))})
	if err != nil || reverse.Value.Number == nil || *reverse.Value.Number != 4000 {
		t.Fatalf("reverse = %#v, error = %v", reverse, err)
	}
}

func TestAutoCapabilityProfileRejectsIdentityAndSemanticConversions(t *testing.T) {
	for _, test := range []struct {
		input, output device.ValueType
		from, to      string
	}{
		{device.ValueTypeNumber, device.ValueTypeNumber, "celsius", "celsius"},
		{device.ValueTypeBool, device.ValueTypeBool, "ratio", "percent"},
		{device.ValueTypeNumber, device.ValueTypeNumber, "celsius", "percent"},
		{device.ValueTypeNumber, device.ValueTypeNumber, "", "percent"},
	} {
		if profile, ok := AutoCapabilityProfile(test.input, test.output, test.from, test.to); ok {
			t.Fatalf("unexpected profile %#v", profile)
		}
	}
}

func TestAutoCapabilityProfilesAreValidAndUnique(t *testing.T) {
	profiles := AutoCapabilityProfiles()
	if len(profiles) != 32 {
		t.Fatalf("profiles = %d, want 32", len(profiles))
	}
	seen := make(map[string]struct{}, len(profiles))
	for _, profile := range profiles {
		if err := Validate(profile); err != nil {
			t.Fatalf("profile %q: %v", profile.ID, err)
		}
		if _, exists := seen[profile.ID]; exists {
			t.Fatalf("duplicate profile %q", profile.ID)
		}
		seen[profile.ID] = struct{}{}
	}
}
