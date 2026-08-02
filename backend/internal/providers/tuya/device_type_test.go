package tuya

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func TestInferDeviceTypeCoversCommonTuyaCategories(t *testing.T) {
	tests := map[string]device.Type{
		"dj":         device.TypeLightbulb,
		"cz":         device.TypeOutlet,
		"kg":         device.TypeSwitch,
		"fs":         device.TypeFan,
		"cl":         device.TypeWindowCovering,
		"kj":         device.TypeAirPurifier,
		"kt":         device.TypeAirConditioner,
		"jsq":        device.TypeWaterHeater,
		"zndb":       device.TypePowerMeter,
		"sd":         device.TypeRobotVacuum,
		"wsdcg":      device.TypeTemperatureHumiditySensor,
		"mcs":        device.TypeContactSensor,
		"pir":        device.TypeMotionSensor,
		"ywbj":       device.TypeLeakSensor,
		"ywb":        device.TypeSmokeSensor,
		"pm2.5":      device.TypeAirQualitySensor,
		"照度":         device.TypeIlluminanceSensor,
		"humidifier": device.TypeHumidifierDehumidifier,
	}
	for category, want := range tests {
		if got := inferDeviceType(category, nil); got != want {
			t.Errorf("category %q = %q, want %q", category, got, want)
		}
	}
}

func TestInferDeviceTypeUsesDPSignalsForUnknownCategory(t *testing.T) {
	if got := inferDeviceType("unknown", map[string]DPSpec{"bright_value": {Code: "bright_value"}}); got != device.TypeLightbulb {
		t.Fatalf("brightness signal = %q", got)
	}
	if got := inferDeviceType("unknown", map[string]DPSpec{
		"switch":    {Code: "switch"},
		"fan_speed": {Code: "fan_speed"},
	}); got != device.TypeFan {
		t.Fatalf("fan signal = %q", got)
	}
	if got := inferDeviceType("unknown", map[string]DPSpec{
		"va_temperature": {Code: "va_temperature"},
		"va_humidity":    {Code: "va_humidity"},
	}); got != device.TypeTemperatureHumiditySensor {
		t.Fatalf("temperature/humidity signal = %q", got)
	}
}
