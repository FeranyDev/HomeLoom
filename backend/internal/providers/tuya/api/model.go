package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// DPType is the type name used by Tuya's data point (DP) specification.
// Tuya has historically used a few spelling variants; the aliases below keep
// callers from having to duplicate that compatibility handling.
type DPType string

const (
	DPTypeBoolean DPType = "Boolean"
	DPTypeBool           = DPTypeBoolean
	DPTypeEnum    DPType = "Enum"
	DPTypeInteger DPType = "Integer"
	DPTypeInt            = DPTypeInteger
	DPTypeNumber  DPType = "Value"
	DPTypeValue          = DPTypeNumber
	DPTypeString  DPType = "String"
	DPTypeJSON    DPType = "Json"
	DPTypeJson           = DPTypeJSON
	DPTypeRaw     DPType = "Raw"
	DPTypeBitmap  DPType = "Bitmap"
)

// Canonical normalizes the case and common aliases returned by different
// Tuya product generations. Unknown values are retained in title case so a
// caller can still expose them as a custom DP instead of losing it.
func (t DPType) Canonical() DPType {
	switch strings.ToLower(strings.TrimSpace(string(t))) {
	case "boolean", "bool":
		return DPTypeBoolean
	case "enum", "enumeration":
		return DPTypeEnum
	case "integer", "int":
		return DPTypeInteger
	case "number", "value", "float", "double":
		return DPTypeNumber
	case "string", "text":
		return DPTypeString
	case "json", "object":
		return DPTypeJSON
	case "raw":
		return DPTypeRaw
	case "bitmap", "bitset":
		return DPTypeBitmap
	default:
		return DPType(strings.TrimSpace(string(t)))
	}
}

// TuyaDevice is the useful, stable subset of the response returned by the
// user-device directory API. Unknown response fields are intentionally not
// modelled: Tuya adds fields regularly and they are not needed for discovery.
type TuyaDevice struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Category     string          `json:"category"`
	CategoryName string          `json:"category_name,omitempty"`
	ProductID    string          `json:"product_id,omitempty"`
	ProductName  string          `json:"product_name,omitempty"`
	ProductKey   string          `json:"product_key,omitempty"`
	Model        string          `json:"model,omitempty"`
	Online       bool            `json:"online"`
	Status       []TuyaStatus    `json:"status,omitempty"`
	LocalKey     string          `json:"local_key,omitempty"`
	UUID         string          `json:"uuid,omitempty"`
	GatewayID    string          `json:"gateway_id,omitempty"`
	OwnerID      string          `json:"owner_id,omitempty"`
	UID          string          `json:"uid,omitempty"`
	AssetID      string          `json:"asset_id,omitempty"`
	RoomID       string          `json:"room_id,omitempty"`
	RoomName     string          `json:"room_name,omitempty"`
	HomeID       string          `json:"home_id,omitempty"`
	HomeName     string          `json:"home_name,omitempty"`
	Timezone     string          `json:"timezone,omitempty"`
	ActiveTime   int64           `json:"active_time,omitempty"`
	UpdateTime   int64           `json:"update_time,omitempty"`
	Schema       string          `json:"schema,omitempty"`
	DeviceType   string          `json:"device_type,omitempty"`
	Sub          bool            `json:"sub,omitempty"`
	Raw          json.RawMessage `json:"-"`
}

// TuyaStatus is one status DP reported by a device. Value intentionally uses
// any: Tuya sends booleans, strings, integer-like numbers and JSON strings in
// the same endpoint. ParseStatusValue applies a DPSpec to obtain a stable
// typed value.
type TuyaStatus struct {
	Code       string    `json:"code"`
	Name       string    `json:"name,omitempty"`
	Type       DPType    `json:"type,omitempty"`
	Value      any       `json:"value"`
	ObservedAt time.Time `json:"observedAt,omitempty"`
}

// Status is a short compatibility spelling for callers that do not need to
// distinguish the wire model from other status sources.
type Status = TuyaStatus

// UnmarshalJSON keeps numeric status values exact enough for scale conversion
// by decoding them as json.Number instead of float64.
func (s *TuyaStatus) UnmarshalJSON(data []byte) error {
	var wire struct {
		Code  string          `json:"code"`
		Name  string          `json:"name"`
		Type  DPType          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	value, err := decodeJSONValue(wire.Value)
	if err != nil {
		return fmt.Errorf("decode Tuya status %q value: %w", wire.Code, err)
	}
	*s = TuyaStatus{Code: wire.Code, Name: wire.Name, Type: wire.Type, Value: value}
	return nil
}

// DPSpec describes one Tuya function or status DP. Values is retained as the
// compact JSON string used by Tuya's API, while the parsed fields provide safe
// access to ranges and enum values without reparsing at every update.
type DPSpec struct {
	Code        string   `json:"code"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"desc,omitempty"`
	Type        DPType   `json:"type"`
	Values      string   `json:"values,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Step        *float64 `json:"step,omitempty"`
	Scale       int      `json:"scale,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	Readable    bool     `json:"readable"`
	Writable    bool     `json:"writable"`
	EnumValues  []string `json:"enumValues,omitempty"`
	// Enum is accepted as a compatibility spelling. EnumValues is canonical.
	Enum      []string `json:"enum,omitempty"`
	MaxLength int      `json:"maxLength,omitempty"`

	readableSet bool
	writableSet bool
}

// UnmarshalJSON accepts Tuya's values field both as the documented JSON
// string and as an object (some newer endpoints return the object directly).
// It parses the object immediately and returns malformed values as an error,
// rather than silently creating a partially usable DP model.
func (s *DPSpec) UnmarshalJSON(data []byte) error {
	var wire struct {
		Code        string          `json:"code"`
		Name        string          `json:"name"`
		Description string          `json:"desc"`
		Type        DPType          `json:"type"`
		Values      json.RawMessage `json:"values"`
		Min         json.RawMessage `json:"min"`
		Max         json.RawMessage `json:"max"`
		Step        json.RawMessage `json:"step"`
		Scale       json.RawMessage `json:"scale"`
		Unit        string          `json:"unit"`
		Readable    *bool           `json:"readable"`
		Writable    *bool           `json:"writable"`
		EnumValues  []string        `json:"enumValues"`
		Enum        []string        `json:"enum"`
		MaxLength   json.RawMessage `json:"maxLength"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	values, err := valuesString(wire.Values)
	if err != nil {
		return fmt.Errorf("decode DP %q values: %w", wire.Code, err)
	}
	result := DPSpec{
		Code: wire.Code, Name: wire.Name, Description: wire.Description, Type: wire.Type,
		Values: values, Unit: wire.Unit, EnumValues: append([]string(nil), wire.EnumValues...),
		Enum: append([]string(nil), wire.Enum...), Readable: wire.Readable != nil && *wire.Readable,
		Writable:    wire.Writable != nil && *wire.Writable,
		readableSet: wire.Readable != nil, writableSet: wire.Writable != nil,
	}
	if result.Type == "" {
		result.Type = inferDPType(values)
	}
	for name, raw := range map[string]json.RawMessage{"min": wire.Min, "max": wire.Max, "step": wire.Step} {
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		number, parseErr := parseNumber(raw)
		if parseErr != nil {
			return fmt.Errorf("decode DP %q %s: %w", wire.Code, name, parseErr)
		}
		switch name {
		case "min":
			result.Min = float64Pointer(number)
		case "max":
			result.Max = float64Pointer(number)
		case "step":
			result.Step = float64Pointer(number)
		}
	}
	if len(wire.Scale) > 0 && !bytes.Equal(bytes.TrimSpace(wire.Scale), []byte("null")) {
		scale, parseErr := parseInteger(wire.Scale)
		if parseErr != nil {
			return fmt.Errorf("decode DP %q scale: %w", wire.Code, parseErr)
		}
		result.Scale = int(scale)
	}
	if len(wire.MaxLength) > 0 && !bytes.Equal(bytes.TrimSpace(wire.MaxLength), []byte("null")) {
		maxLength, parseErr := parseInteger(wire.MaxLength)
		if parseErr != nil || maxLength < 0 {
			if parseErr == nil {
				parseErr = errors.New("must not be negative")
			}
			return fmt.Errorf("decode DP %q maxLength: %w", wire.Code, parseErr)
		}
		result.MaxLength = int(maxLength)
	}
	if err := ParseSpecificationValues(&result); err != nil {
		return fmt.Errorf("parse DP %q values: %w", wire.Code, err)
	}
	result.normalizeEnum()
	*s = result
	return nil
}

// Normalize makes a DPSpec built programmatically behave like one decoded
// from JSON. It is deliberately idempotent and does not infer permissions.
func (s *DPSpec) Normalize() error {
	if s == nil {
		return errors.New("nil DP specification")
	}
	if err := ParseSpecificationValues(s); err != nil {
		return err
	}
	s.Type = s.Type.Canonical()
	s.normalizeEnum()
	return nil
}

func (s *DPSpec) normalizeEnum() {
	if len(s.EnumValues) == 0 && len(s.Enum) > 0 {
		s.EnumValues = append([]string(nil), s.Enum...)
	}
	if len(s.Enum) == 0 && len(s.EnumValues) > 0 {
		s.Enum = append([]string(nil), s.EnumValues...)
	}
}

// SourceAttribute is the normalized source catalog entry consumed by the
// provider publisher. It intentionally contains no HomeLoom-specific device
// capability mapping; unknown DPs remain available as custom attributes.
type SourceAttribute struct {
	Code       string   `json:"code"`
	Name       string   `json:"name,omitempty"`
	Type       DPType   `json:"type"`
	Unit       string   `json:"unit,omitempty"`
	Min        *float64 `json:"min,omitempty"`
	Max        *float64 `json:"max,omitempty"`
	Step       *float64 `json:"step,omitempty"`
	Scale      int      `json:"scale,omitempty"`
	Readable   bool     `json:"readable"`
	Writable   bool     `json:"writable"`
	EnumValues []string `json:"enumValues,omitempty"`
	Enum       []string `json:"enum,omitempty"`
	MaxLength  int      `json:"maxLength,omitempty"`
}

func (s DPSpec) SourceAttribute() SourceAttribute {
	s.normalizeEnum()
	return SourceAttribute{
		Code: s.Code, Name: s.Name, Type: s.Type.Canonical(), Unit: s.Unit,
		Min: cloneFloat64(s.Min), Max: cloneFloat64(s.Max), Step: cloneFloat64(s.Step),
		Scale: s.Scale, Readable: s.Readable, Writable: s.Writable,
		EnumValues: append([]string(nil), s.EnumValues...), Enum: append([]string(nil), s.EnumValues...), MaxLength: s.MaxLength,
	}
}

// Specification is returned by the device specification endpoint.
type Specification struct {
	Category   string   `json:"category"`
	Functions  []DPSpec `json:"functions"`
	Status     []DPSpec `json:"status"`
	Properties []DPSpec `json:"properties,omitempty"`
}

// TuyaSpecification is the descriptive name used by the provider package.
type TuyaSpecification = Specification

// Token is the result of a token acquisition or refresh call.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	UID          string `json:"uid,omitempty"`
	ExpiresIn    int64  `json:"expire_time,omitempty"`
	// ExpireTime is retained as an alias for callers using Tuya's field name.
	ExpireTime int64 `json:"-"`
}

func (t *Token) UnmarshalJSON(data []byte) error {
	var wire struct {
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		UID          string          `json:"uid"`
		ExpiresIn    json.RawMessage `json:"expire_time"`
		ExpiresInAlt json.RawMessage `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	rawExpiry := wire.ExpiresIn
	if len(rawExpiry) == 0 {
		rawExpiry = wire.ExpiresInAlt
	}
	expires, err := parseInteger(rawExpiry)
	if len(rawExpiry) == 0 || bytes.Equal(bytes.TrimSpace(rawExpiry), []byte("null")) {
		expires, err = 0, nil
	}
	if err != nil {
		return fmt.Errorf("decode token expiry: %w", err)
	}
	*t = Token{AccessToken: wire.AccessToken, RefreshToken: wire.RefreshToken, UID: wire.UID, ExpiresIn: expires, ExpireTime: expires}
	return nil
}

// Command is one Tuya DP command sent to a device.
type Command struct {
	Code  string `json:"code"`
	Value any    `json:"value"`
}

type TuyaCommand = Command

func valuesString(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return "", err
		}
		return strings.TrimSpace(text), nil
	}
	var object any
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return "", err
	}
	compact, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return string(compact), nil
}

func inferDPType(values string) DPType {
	trimmed := strings.TrimSpace(values)
	if trimmed == "" || trimmed == "{}" {
		return ""
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &object) != nil {
		return ""
	}
	if _, ok := object["range"]; ok {
		return DPTypeEnum
	}
	if _, ok := object["min"]; ok {
		return DPTypeInteger
	}
	if _, ok := object["maxlen"]; ok {
		return DPTypeString
	}
	return ""
}

// ParseSpecificationValues parses the values JSON returned by Tuya. It is
// safe to call repeatedly after a quirk patch.
func ParseSpecificationValues(spec *DPSpec) error {
	if spec == nil {
		return errors.New("nil DP specification")
	}
	text := strings.TrimSpace(spec.Values)
	if text == "" || text == "{}" || text == "null" {
		if spec.Type == "" {
			spec.Type = inferDPType(text)
		}
		spec.Type = spec.Type.Canonical()
		spec.normalizeEnum()
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &object); err != nil {
		return fmt.Errorf("values must be a JSON object: %w", err)
	}
	for key, raw := range object {
		keyLower := strings.ToLower(key)
		if keyLower == "min" || keyLower == "max" || keyLower == "step" {
			number, err := parseNumber(raw)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			switch keyLower {
			case "min":
				spec.Min = float64Pointer(number)
			case "max":
				spec.Max = float64Pointer(number)
			case "step":
				spec.Step = float64Pointer(number)
			}
			continue
		}
		if keyLower == "scale" {
			scale, err := parseInteger(raw)
			if err != nil {
				return fmt.Errorf("scale: %w", err)
			}
			spec.Scale = int(scale)
			continue
		}
		if keyLower == "unit" {
			var unit string
			if err := json.Unmarshal(raw, &unit); err != nil {
				return fmt.Errorf("unit: %w", err)
			}
			spec.Unit = strings.TrimSpace(unit)
			continue
		}
		if keyLower == "maxlen" || keyLower == "maxlength" {
			maxLength, err := parseInteger(raw)
			if err != nil || maxLength < 0 {
				if err == nil {
					err = errors.New("must not be negative")
				}
				return fmt.Errorf("maxlen: %w", err)
			}
			spec.MaxLength = int(maxLength)
			continue
		}
		if keyLower == "range" || keyLower == "enum" {
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				return fmt.Errorf("enum range: %w", err)
			}
			parsed := make([]string, 0, len(values))
			for _, value := range values {
				text, err := scalarString(value)
				if err != nil {
					return fmt.Errorf("enum value: %w", err)
				}
				parsed = append(parsed, text)
			}
			spec.EnumValues = parsed
			spec.Enum = append([]string(nil), parsed...)
		}
	}
	spec.Type = spec.Type.Canonical()
	spec.normalizeEnum()
	return nil
}

// ParseStatusValue converts a raw status value according to a DP spec. The
// returned types are bool, string, int64/float64, or decoded JSON values.
func ParseStatusValue(spec DPSpec, raw any) (any, error) {
	if err := spec.Normalize(); err != nil {
		return nil, err
	}
	return parseValue(spec, raw)
}

// ParseDPValue is an alias useful to command and status paths alike.
func ParseDPValue(spec DPSpec, raw any) (any, error) { return ParseStatusValue(spec, raw) }

func parseValue(spec DPSpec, raw any) (any, error) {
	switch spec.Type.Canonical() {
	case DPTypeBoolean:
		return ParseBoolean(raw)
	case DPTypeEnum:
		return ParseEnum(raw, spec.EnumValues)
	case DPTypeInteger, DPTypeNumber, DPTypeBitmap:
		number, err := ScaledNumber(raw, spec.Scale)
		if err != nil {
			return nil, err
		}
		if spec.Type.Canonical() == DPTypeBitmap || spec.Scale == 0 {
			if math.Trunc(number) != number || number < math.MinInt64 || number > math.MaxInt64 {
				return nil, fmt.Errorf("integer value %v is outside int64 range", number)
			}
			return int64(number), nil
		}
		return number, nil
	case DPTypeString:
		return ParseString(raw)
	case DPTypeJSON, DPTypeRaw:
		return ParseJSONValue(raw)
	default:
		// Unknown DPs are retained without coercion. This is important for
		// forward compatibility with product-specific types.
		return normalizeRawValue(raw)
	}
}

func ParseBoolean(raw any) (bool, error) {
	value, err := normalizeRawValue(raw)
	if err != nil {
		return false, err
	}
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "on", "yes":
			return true, nil
		case "false", "0", "off", "no":
			return false, nil
		}
	case json.Number:
		if typed == "1" {
			return true, nil
		}
		if typed == "0" {
			return false, nil
		}
	case float64:
		if typed == 1 {
			return true, nil
		}
		if typed == 0 {
			return false, nil
		}
	case int, int8, int16, int32, int64:
		if reflectInt64(value) == 1 {
			return true, nil
		}
		if reflectInt64(value) == 0 {
			return false, nil
		}
	}
	return false, fmt.Errorf("cannot parse %v as boolean", raw)
}

func ParseEnum(raw any, allowed []string) (string, error) {
	text, err := scalarStringFromAny(raw)
	if err != nil {
		return "", err
	}
	if len(allowed) == 0 {
		return text, nil
	}
	for _, item := range allowed {
		if text == item {
			return text, nil
		}
	}
	return "", fmt.Errorf("enum value %q is not in %v", text, allowed)
}

func ParseString(raw any) (string, error) { return scalarStringFromAny(raw) }

// ParseJSONValue decodes an object/array/primitive. If a Tuya endpoint wraps
// JSON in a JSON string, the inner JSON is decoded once more when valid; an
// ordinary string remains a string.
func ParseJSONValue(raw any) (any, error) {
	value, err := normalizeRawValue(raw)
	if err != nil {
		return nil, err
	}
	if text, ok := value.(string); ok {
		trimmed := strings.TrimSpace(text)
		if trimmed != "" && json.Valid([]byte(trimmed)) {
			return decodeJSONValue([]byte(trimmed))
		}
	}
	return value, nil
}

// ScaledNumber converts a Tuya raw numeric DP into its physical value. Tuya
// encodes scale as a decimal-place count: raw 215 with scale 1 is 21.5.
func ScaledNumber(raw any, scale int) (float64, error) {
	number, err := numberFromAny(raw)
	if err != nil {
		return 0, err
	}
	if scale < -308 || scale > 308 {
		return 0, fmt.Errorf("scale %d is outside the safe range -308..308", scale)
	}
	result := number / math.Pow10(scale)
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return 0, errors.New("scaled number is not finite")
	}
	return result, nil
}

func ScaleValue(value float64, scale int) float64 {
	if scale < -308 || scale > 308 {
		return math.NaN()
	}
	return value / math.Pow10(scale)
}

func numberFromAny(raw any) (float64, error) {
	value, err := normalizeRawValue(raw)
	if err != nil {
		return 0, err
	}
	switch typed := value.(type) {
	case json.Number:
		return strconv.ParseFloat(string(typed), 64)
	case float64:
		return typed, finiteNumber(typed)
	case float32:
		return float64(typed), finiteNumber(float64(typed))
	case int:
		return float64(typed), nil
	case int8:
		return float64(typed), nil
	case int16:
		return float64(typed), nil
	case int32:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case uint:
		return float64(typed), nil
	case uint8:
		return float64(typed), nil
	case uint16:
		return float64(typed), nil
	case uint32:
		return float64(typed), nil
	case uint64:
		return float64(typed), nil
	case string:
		number, parseErr := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if parseErr != nil {
			return 0, fmt.Errorf("%q is not numeric: %w", typed, parseErr)
		}
		return number, finiteNumber(number)
	default:
		return 0, fmt.Errorf("%T is not numeric", raw)
	}
}

func parseNumber(raw json.RawMessage) (float64, error) {
	value, err := decodeJSONValue(raw)
	if err != nil {
		return 0, err
	}
	return numberFromAny(value)
}

func parseInteger(raw json.RawMessage) (int64, error) {
	value, err := decodeJSONValue(raw)
	if err != nil {
		return 0, err
	}
	number, err := numberFromAny(value)
	if err != nil {
		return 0, err
	}
	if math.Trunc(number) != number || number < math.MinInt64 || number > math.MaxInt64 {
		return 0, errors.New("must be an integer")
	}
	return int64(number), nil
}

func scalarString(value json.RawMessage) (string, error) {
	decoded, err := decodeJSONValue(value)
	if err != nil {
		return "", err
	}
	return scalarStringFromAny(decoded)
}

func scalarStringFromAny(raw any) (string, error) {
	value, err := normalizeRawValue(raw)
	if err != nil {
		return "", err
	}
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return "", errors.New("value must not be empty")
		}
		return typed, nil
	case json.Number:
		return string(typed), nil
	case float64:
		if err := finiteNumber(typed); err != nil {
			return "", err
		}
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(typed), nil
	default:
		return "", fmt.Errorf("%T is not a scalar", raw)
	}
}

func normalizeRawValue(raw any) (any, error) {
	if raw == nil {
		return nil, errors.New("value is nil")
	}
	if message, ok := raw.(json.RawMessage); ok {
		return decodeJSONValue(message)
	}
	if bytesValue, ok := raw.([]byte); ok {
		return decodeJSONValue(bytesValue)
	}
	return raw, nil
}

func decodeJSONValue(raw []byte) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("JSON value is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func finiteNumber(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return errors.New("number is not finite")
	}
	return nil
}

func reflectInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	default:
		return math.MinInt64
	}
}

func float64Pointer(value float64) *float64 { return &value }

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
