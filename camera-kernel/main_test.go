package main

import (
	"reflect"
	"testing"
)

func TestCameraKernelCapabilityAllowList(t *testing.T) {
	var names []string
	for _, module := range scopedModules() {
		names = append(names, module.name)
		if module.init == nil {
			t.Fatalf("capability %q has no initializer", module.name)
		}
	}
	want := []string{
		"api", "streams", "rtsp", "onvif-input", "xiaomi-miss",
		"mp4-preview", "exec-allowlist", "ffmpeg", "homekit-output", "matter-webrtc-output", "srtp",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("capability allow-list = %v, want %v", names, want)
	}
	for _, excluded := range []string{
		"homekit-input", "webrtc", "rtmp", "hls", "dvr", "tapo",
		"tuya", "ring", "wyze", "generic-web-ui",
	} {
		if hasCapability(excluded) {
			t.Fatalf("excluded capability %q is enabled", excluded)
		}
	}
}
