package tuya

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestSpecificationValuesDecodeIntoDynamicDPSpec(t *testing.T) {
	var specification TuyaSpecification
	if err := json.Unmarshal([]byte(`{
		"category":"dj",
		"functions":[
			{"code":"switch_led","name":"Switch","type":"Boolean","values":"{}"},
			{"code":"bright_value","type":"Integer","values":"{\"min\":10,\"max\":1000,\"scale\":1,\"step\":5,\"unit\":\"%\"}"},
			{"code":"work_mode","type":"Enum","values":{"range":["auto","manual"]}}
		],
		"status":[{"code":"temperature","type":"Integer","values":"{\"min\":-400,\"max\":1200,\"scale\":1,\"unit\":\"℃\"}"}]
	}`), &specification); err != nil {
		t.Fatal(err)
	}
	if specification.Category != "dj" || len(specification.Functions) != 3 || len(specification.Status) != 1 {
		t.Fatalf("specification = %#v", specification)
	}
	bright := specification.Functions[1]
	if bright.Min == nil || *bright.Min != 10 || bright.Max == nil || *bright.Max != 1000 || bright.Step == nil || *bright.Step != 5 || bright.Scale != 1 || bright.Unit != "%" {
		t.Fatalf("numeric DP = %#v", bright)
	}
	mode := specification.Functions[2]
	if mode.Type != DPTypeEnum || strings.Join(mode.EnumValues, ",") != "auto,manual" {
		t.Fatalf("enum DP = %#v", mode)
	}
	if specification.Status[0].Min == nil || *specification.Status[0].Min != -400 || specification.Status[0].Scale != 1 {
		t.Fatalf("status DP = %#v", specification.Status[0])
	}
}

func TestDPSpecNormalizeAndSourceAttributeCloneMetadata(t *testing.T) {
	minimum, maximum := 0.0, 100.0
	spec := DPSpec{Code: "brightness", Type: DPTypeInteger, Values: `{"min":0,"max":100,"scale":0,"range":["unused"]}`, Min: &minimum, Max: &maximum}
	if err := parseSpecificationValues(&spec); err != nil {
		t.Fatal(err)
	}
	attribute := spec.SourceAttribute()
	if attribute.Code != "brightness" || attribute.Type != DPTypeInteger || attribute.Min == nil || attribute.Max == nil {
		t.Fatalf("source attribute = %#v", attribute)
	}
	*attribute.Min = 99
	if *spec.Min != 0 {
		t.Fatal("source attribute shares mutable range pointer")
	}
}

func TestScaleAndStatusValueConversions(t *testing.T) {
	if got, err := scaledNumber(json.Number("215"), 1); err != nil || got != 21.5 {
		t.Fatalf("scaledNumber = %v, %v", got, err)
	}
	if got := scaleValue(215, 1); got != 21.5 {
		t.Fatalf("scaleValue = %v", got)
	}
	if !math.IsNaN(scaleValue(1, 309)) {
		t.Fatal("unsafe scale did not return NaN")
	}

	boolean, err := parseStatusValue(DPSpec{Type: DPTypeBoolean}, json.RawMessage(`"false"`))
	if err != nil || boolean != false {
		t.Fatalf("boolean status = %#v, %v", boolean, err)
	}
	enum, err := parseStatusValue(DPSpec{Type: DPTypeEnum, EnumValues: []string{"auto", "manual"}}, "manual")
	if err != nil || enum != "manual" {
		t.Fatalf("enum status = %#v, %v", enum, err)
	}
	integer, err := parseStatusValue(DPSpec{Type: DPTypeInteger, Scale: 1}, json.Number("235"))
	if err != nil || integer != 23.5 {
		t.Fatalf("scaled integer status = %#v, %v", integer, err)
	}
	jsonValue, err := parseStatusValue(DPSpec{Type: DPTypeJSON}, `{"preset":2}`)
	if err != nil {
		t.Fatal(err)
	}
	object, ok := jsonValue.(map[string]any)
	if !ok || object["preset"] != json.Number("2") {
		t.Fatalf("JSON status = %#v", jsonValue)
	}
}

func TestStatusValueParsingRejectsUnsafeOrInvalidValues(t *testing.T) {
	for name, value := range map[string]any{
		"bad boolean": "maybe",
		"bad enum":    "unknown",
		"bad number":  "not-a-number",
	} {
		t.Run(name, func(t *testing.T) {
			spec := DPSpec{Type: DPTypeBoolean}
			if name == "bad enum" {
				spec = DPSpec{Type: DPTypeEnum, EnumValues: []string{"known"}}
			}
			if name == "bad number" {
				spec = DPSpec{Type: DPTypeInteger}
			}
			if _, err := parseStatusValue(spec, value); err == nil {
				t.Fatalf("accepted invalid value %#v", value)
			}
		})
	}
	var malformed DPSpec
	if err := json.Unmarshal([]byte(`{"code":"broken","type":"Integer","values":"not-json"}`), &malformed); err == nil {
		t.Fatal("accepted malformed values JSON")
	}
}
