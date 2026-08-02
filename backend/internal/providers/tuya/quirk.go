package tuya

import "strings"

// applyQuirks repairs a device specification before it becomes a source
// attribute catalog. It deliberately does not assign HomeLoom semantics; a
// quirk only corrects malformed Tuya metadata.
func applyQuirks(spec *TuyaSpecification, productID string, quirks []QuirkConfig) {
	if spec == nil {
		return
	}
	for _, quirk := range quirks {
		if strings.TrimSpace(quirk.ProductID) != "" && !strings.EqualFold(strings.TrimSpace(quirk.ProductID), productID) {
			continue
		}
		for _, patch := range quirk.Patches {
			applyQuirkPatch(spec, patch)
		}
	}
}

func applyQuirkPatch(spec *TuyaSpecification, patch QuirkPatch) {
	code := strings.TrimSpace(patch.Code)
	if code == "" {
		return
	}
	apply := func(item *DPSpec) {
		if patch.Type != "" {
			item.Type = patch.Type
		}
		if patch.Min != nil {
			item.Min = patch.Min
		}
		if patch.Max != nil {
			item.Max = patch.Max
		}
		if patch.Step != nil {
			item.Step = patch.Step
		}
		if patch.Scale != nil {
			item.Scale = *patch.Scale
		}
		if patch.Unit != "" {
			item.Unit = patch.Unit
		}
		if patch.Readable != nil {
			item.Readable = *patch.Readable
		}
		if patch.Writable != nil {
			item.Writable = *patch.Writable
		}
		if patch.Enum != nil {
			item.EnumValues = append([]string(nil), patch.Enum...)
		}
		if patch.NewCode != "" {
			item.Code = strings.TrimSpace(patch.NewCode)
		}
	}

	if patch.Operation == "remove" || patch.Operation == "delete" {
		removeDPSpec(&spec.Functions, code)
		removeDPSpec(&spec.Status, code)
		return
	}
	for index := range spec.Functions {
		if strings.EqualFold(spec.Functions[index].Code, code) {
			apply(&spec.Functions[index])
		}
	}
	for index := range spec.Status {
		if strings.EqualFold(spec.Status[index].Code, code) {
			apply(&spec.Status[index])
		}
	}
}

func removeDPSpec(items *[]DPSpec, code string) {
	filtered := (*items)[:0]
	for _, item := range *items {
		if !strings.EqualFold(item.Code, code) {
			filtered = append(filtered, item)
		}
	}
	*items = filtered
}
