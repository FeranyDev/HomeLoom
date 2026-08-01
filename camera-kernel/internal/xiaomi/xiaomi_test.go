package xiaomi

import "testing"

func TestValidateMISSURLAcceptsOnlyCompletePreauthorization(t *testing.T) {
	valid := "xiaomi://192.0.2.20?did=1&model=camera&vendor=cs2&client_public=aa&client_private=bb&device_public=cc&sign=dd"
	if err := validateMISSURL(valid); err != nil {
		t.Fatalf("valid preauthorized source rejected: %v", err)
	}
	for _, source := range []string{
		"xiaomi://account:cn@192.0.2.20?did=1&vendor=cs2&client_public=aa&client_private=bb&device_public=cc&sign=dd",
		"xiaomi://192.0.2.20?did=1",
		"rtsp://192.0.2.20/live",
	} {
		if err := validateMISSURL(source); err == nil {
			t.Fatalf("unsupported Xiaomi source accepted: %s", source)
		}
	}
}
