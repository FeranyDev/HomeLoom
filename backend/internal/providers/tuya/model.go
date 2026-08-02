package tuya

// The REST client keeps wire models in the api subpackage so the provider can
// inject it without an import cycle. These aliases make the same models part
// of the provider package, which is where publisher and quirk code consume
// them.
import tuyaapi "github.com/feranydev/homeloom/backend/internal/providers/tuya/api"

type DPType = tuyaapi.DPType

const (
	DPTypeBoolean = tuyaapi.DPTypeBoolean
	DPTypeBool    = tuyaapi.DPTypeBool
	DPTypeEnum    = tuyaapi.DPTypeEnum
	DPTypeInteger = tuyaapi.DPTypeInteger
	DPTypeInt     = tuyaapi.DPTypeInt
	DPTypeNumber  = tuyaapi.DPTypeNumber
	DPTypeValue   = tuyaapi.DPTypeValue
	DPTypeString  = tuyaapi.DPTypeString
	DPTypeJSON    = tuyaapi.DPTypeJSON
	DPTypeJson    = tuyaapi.DPTypeJson
	DPTypeRaw     = tuyaapi.DPTypeRaw
	DPTypeBitmap  = tuyaapi.DPTypeBitmap
)

type TuyaDevice = tuyaapi.TuyaDevice
type TuyaStatus = tuyaapi.TuyaStatus
type Status = tuyaapi.Status
type DPSpec = tuyaapi.DPSpec
type SourceAttribute = tuyaapi.SourceAttribute
type Specification = tuyaapi.Specification
type TuyaSpecification = tuyaapi.TuyaSpecification
type TuyaCommand = tuyaapi.TuyaCommand
type Command = tuyaapi.Command
type Token = tuyaapi.Token

// DPStatus is the provider-neutral status event used by the optional Tuya
// message channel. REST status values use TuyaStatus; the event adds the
// source timestamp when one is available.
type DPStatus = tuyaapi.DPStatus

func parseSpecificationValues(spec *DPSpec) error {
	return tuyaapi.ParseSpecificationValues(spec)
}

func parseStatusValue(spec DPSpec, raw any) (any, error) {
	return tuyaapi.ParseStatusValue(spec, raw)
}

func parseDPValue(spec DPSpec, raw any) (any, error) {
	return tuyaapi.ParseDPValue(spec, raw)
}

func parseBoolean(raw any) (bool, error) { return tuyaapi.ParseBoolean(raw) }

func parseEnum(raw any, allowed []string) (string, error) {
	return tuyaapi.ParseEnum(raw, allowed)
}

func parseJSONValue(raw any) (any, error) { return tuyaapi.ParseJSONValue(raw) }

func scaledNumber(raw any, scale int) (float64, error) {
	return tuyaapi.ScaledNumber(raw, scale)
}

// scaleValue is the compact helper used by provider code for a known numeric
// raw value. Invalid scale values return NaN; callers that need an error can
// use scaledNumber.
func scaleValue(value float64, scale int) float64 {
	return tuyaapi.ScaleValue(value, scale)
}
