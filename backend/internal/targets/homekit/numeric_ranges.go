package homekit

import (
	"math"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	"go.uber.org/zap"
)

var homeKitTargetCharacteristicTypes = map[string][]string{
	"Lightbulb.Brightness":                           {characteristic.TypeBrightness},
	"Lightbulb.ColorTemperature":                     {characteristic.TypeColorTemperature},
	"Lightbulb.Hue":                                  {characteristic.TypeHue},
	"Lightbulb.Saturation":                           {characteristic.TypeSaturation},
	"TemperatureSensor.CurrentTemperature":           {characteristic.TypeCurrentTemperature},
	"HumiditySensor.CurrentRelativeHumidity":         {characteristic.TypeCurrentRelativeHumidity},
	"BatteryService.BatteryLevel":                    {characteristic.TypeBatteryLevel},
	"FanV2.RotationSpeed":                            {characteristic.TypeRotationSpeed},
	"AirPurifier.RotationSpeed":                      {characteristic.TypeRotationSpeed},
	"AirQualitySensor.PM2.5Density":                  {characteristic.TypePM2_5Density},
	"AirQualitySensor.PM10Density":                   {characteristic.TypePM10Density},
	"AirQualitySensor.VOCDensity":                    {characteristic.TypeVOCDensity},
	"AirQualitySensor.CarbonDioxideLevel":            {characteristic.TypeCarbonDioxideLevel},
	"AirQualitySensor.NitrogenDioxideDensity":        {characteristic.TypeNitrogenDioxideDensity},
	"AirQualitySensor.OzoneDensity":                  {characteristic.TypeOzoneDensity},
	"FilterMaintenance.FilterLifeLevel":              {characteristic.TypeFilterLifeLevel},
	"WindowCovering.CurrentPosition":                 {characteristic.TypeCurrentPosition},
	"WindowCovering.TargetPosition":                  {characteristic.TypeTargetPosition},
	"LightSensor.CurrentAmbientLightLevel":           {characteristic.TypeCurrentAmbientLightLevel},
	"CarbonMonoxideSensor.CarbonMonoxideLevel":       {characteristic.TypeCarbonMonoxideLevel},
	"CarbonMonoxideSensor.CarbonMonoxidePeakLevel":   {characteristic.TypeCarbonMonoxidePeakLevel},
	"CarbonDioxideSensor.CarbonDioxideLevel":         {characteristic.TypeCarbonDioxideLevel},
	"CarbonDioxideSensor.CarbonDioxidePeakLevel":     {characteristic.TypeCarbonDioxidePeakLevel},
	"Thermostat.CurrentTemperature":                  {characteristic.TypeCurrentTemperature},
	"Thermostat.TargetTemperature":                   {characteristic.TypeTargetTemperature},
	"Thermostat.HeatingThresholdTemperature":         {characteristic.TypeHeatingThresholdTemperature},
	"Thermostat.CoolingThresholdTemperature":         {characteristic.TypeCoolingThresholdTemperature},
	"Thermostat.CurrentRelativeHumidity":             {characteristic.TypeCurrentRelativeHumidity},
	"HeaterCooler.CurrentTemperature":                {characteristic.TypeCurrentTemperature},
	"HeaterCooler.TargetTemperature":                 {characteristic.TypeHeatingThresholdTemperature, characteristic.TypeCoolingThresholdTemperature},
	"HeaterCooler.HeatingThresholdTemperature":       {characteristic.TypeHeatingThresholdTemperature},
	"HeaterCooler.CoolingThresholdTemperature":       {characteristic.TypeCoolingThresholdTemperature},
	"HeaterCooler.RotationSpeed":                     {characteristic.TypeRotationSpeed},
	"HumidifierDehumidifier.CurrentRelativeHumidity": {characteristic.TypeCurrentRelativeHumidity},
	"HumidifierDehumidifier.TargetHumidity":          {characteristic.TypeRelativeHumidityHumidifierThreshold, characteristic.TypeRelativeHumidityDehumidifierThreshold},
	"HumidifierDehumidifier.WaterLevel":              {characteristic.TypeWaterLevel},
	"Valve.SetDuration":                              {characteristic.TypeSetDuration},
	"Valve.RemainingDuration":                        {characteristic.TypeRemainingDuration},
	"Speaker.Volume":                                 {characteristic.TypeVolume},
	"Television.Volume":                              {characteristic.TypeVolume},
}

func configureAccessoryNumericRanges(item *accessory.A, source device.Device, logger *zap.Logger) {
	contract, found := mapping.HomeKitConsumerContract(source.Type)
	if !found {
		return
	}
	for _, parameter := range contract.Parameters {
		types := homeKitTargetCharacteristicTypes[parameter.Target]
		if len(types) == 0 {
			continue
		}
		property, propertyFound := source.Property(parameter.Source.EndpointID, parameter.Source.CapabilityID, parameter.Source.PropertyID)
		if !propertyFound || (property.Definition.Type != device.ValueTypeInt && property.Definition.Type != device.ValueTypeNumber) {
			continue
		}
		definition := property.Definition
		for _, service := range item.Ss {
			filtered := service.Cs[:0]
			for _, current := range service.Cs {
				if !containsCharacteristicType(types, current.Type) {
					filtered = append(filtered, current)
					continue
				}
				if configureNumericCharacteristicRange(current, definition) {
					filtered = append(filtered, current)
					continue
				}
				logger.Warn("device numeric range does not overlap HomeKit; characteristic omitted",
					zap.String("device_id", source.ID), zap.String("property", parameter.Source.Key()), zap.String("characteristic", parameter.Target),
					zap.Any("min", property.Definition.Min), zap.Any("max", property.Definition.Max))
			}
			service.Cs = filtered
		}
	}
}

func containsCharacteristicType(types []string, current string) bool {
	for _, candidate := range types {
		if candidate == current {
			return true
		}
	}
	return false
}

func configureNumericCharacteristicRange(current *characteristic.C, definition device.PropertyDefinition) bool {
	switch current.Format {
	case characteristic.FormatFloat:
		minimum, minOK := current.MinVal.(float64)
		maximum, maxOK := current.MaxVal.(float64)
		if !minOK || !maxOK {
			return true
		}
		if definition.Min != nil {
			minimum = max(minimum, *definition.Min)
		}
		if definition.Max != nil {
			maximum = min(maximum, *definition.Max)
		}
		if minimum > maximum {
			return false
		}
		current.MinVal, current.MaxVal = minimum, maximum
		if definition.Step != nil && *definition.Step > 0 {
			if step, ok := current.StepVal.(float64); !ok || *definition.Step > step {
				current.StepVal = *definition.Step
			}
		}
	case characteristic.FormatUInt8, characteristic.FormatUInt16, characteristic.FormatUInt32, characteristic.FormatUInt64, characteristic.FormatInt32:
		minimum, minOK := current.MinVal.(int)
		maximum, maxOK := current.MaxVal.(int)
		if !minOK || !maxOK {
			return true
		}
		if definition.Min != nil {
			minimum = max(minimum, int(math.Ceil(*definition.Min)))
		}
		if definition.Max != nil {
			maximum = min(maximum, int(math.Floor(*definition.Max)))
		}
		if minimum > maximum {
			return false
		}
		current.MinVal, current.MaxVal = minimum, maximum
		if definition.Step != nil && *definition.Step > 0 {
			step := max(1, int(math.Ceil(*definition.Step)))
			if currentStep, ok := current.StepVal.(int); !ok || step > currentStep {
				current.StepVal = step
			}
		}
	default:
		return true
	}
	if current.Val != nil {
		_, _ = current.SetValueRequest(current.Val, nil)
	}
	return true
}
