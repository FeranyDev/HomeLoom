package mapping

import (
	"fmt"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// AutoCapabilityProfile returns the deterministic, read-only conversion that
// bridges two compatible numeric capability definitions. It deliberately
// covers only unit changes with an unambiguous inverse; semantic conversions
// (for example, an enum or a vendor-specific range) still need a user-owned
// Profile.
//
// The returned ID is stable so a generated profile can safely be selected by
// the mapping UI and stored in a normal Binding.
func AutoCapabilityProfile(inputType, outputType device.ValueType, fromUnit, toUnit string) (Profile, bool) {
	if !numericType(inputType) || !numericType(outputType) || fromUnit == "" || toUnit == "" || fromUnit == toUnit || !unitConversionSupported(fromUnit, toUnit) {
		return Profile{}, false
	}

	transforms := []Transform{{Type: TransformUnit, FromUnit: fromUnit, ToUnit: toUnit}}
	if outputType == device.ValueTypeInt {
		// Unit conversions produce a number. A deterministic nearest-integer
		// round keeps generated integer targets reversible under the existing
		// Profile reverse semantics.
		transforms = append(transforms, Transform{Type: TransformRound, Mode: "nearest"})
	}
	profile := Profile{
		SchemaVersion: SchemaVersion,
		ID:            fmt.Sprintf("builtin-capability-%s-to-%s-%s-to-%s", fromUnit, toUnit, inputType, outputType),
		Version:       1,
		Kind:          KindCapability,
		InputType:     inputType,
		OutputType:    outputType,
		Transforms:    transforms,
	}
	if err := Validate(profile); err != nil {
		return Profile{}, false
	}
	return profile, true
}

// AutoCapabilityProfiles enumerates all supported unit routes and numeric
// endpoint types. It is used to register generated Capability Profiles as
// built-ins, rather than relying on an implicit conversion at runtime.
func AutoCapabilityProfiles() []Profile {
	routes := [][2]string{
		{"celsius", "fahrenheit"}, {"fahrenheit", "celsius"},
		{"celsius", "kelvin"}, {"kelvin", "celsius"},
		{"kelvin", "mired"}, {"mired", "kelvin"},
		{"ratio", "percent"}, {"percent", "ratio"},
	}
	types := []device.ValueType{device.ValueTypeInt, device.ValueTypeNumber}
	profiles := make([]Profile, 0, len(routes)*len(types)*len(types))
	for _, route := range routes {
		for _, inputType := range types {
			for _, outputType := range types {
				profile, ok := AutoCapabilityProfile(inputType, outputType, route[0], route[1])
				if ok {
					profiles = append(profiles, profile)
				}
			}
		}
	}
	return profiles
}
