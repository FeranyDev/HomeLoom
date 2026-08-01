// HomeLoom Camera Kernel is a deliberately constrained fork of go2rtc v1.9.14.
//
// The initialization list below is the executable capability boundary. Do not
// add a protocol without first extending HomeLoom's Camera Provider contract,
// threat model, tests, and implementation plan.
package main

import (
	"slices"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/exec"
	"github.com/AlexxIT/go2rtc/internal/ffmpeg"
	"github.com/AlexxIT/go2rtc/internal/homekit"
	"github.com/AlexxIT/go2rtc/internal/matterwebrtc"
	"github.com/AlexxIT/go2rtc/internal/mp4"
	"github.com/AlexxIT/go2rtc/internal/onvif"
	"github.com/AlexxIT/go2rtc/internal/rtsp"
	"github.com/AlexxIT/go2rtc/internal/srtp"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/internal/xiaomi"
	"github.com/AlexxIT/go2rtc/pkg/shell"
)

type kernelModule struct {
	name string
	init func()
}

func scopedModules() []kernelModule {
	return []kernelModule{
		{name: "api", init: api.Init},
		{name: "streams", init: streams.Init},
		{name: "rtsp", init: rtsp.Init},
		{name: "onvif-input", init: onvif.Init},
		{name: "xiaomi-miss", init: xiaomi.Init},
		{name: "mp4-preview", init: mp4.Init},
		{name: "exec-allowlist", init: exec.Init},
		{name: "ffmpeg", init: ffmpeg.Init},
		{name: "homekit-output", init: homekit.Init},
		{name: "matter-webrtc-output", init: matterwebrtc.Init},
		{name: "srtp", init: srtp.Init},
	}
}

func main() {
	app.Version = "1.9.14-homeloom.1"

	app.Init()
	for _, module := range scopedModules() {
		module.init()
	}

	shell.RunUntilSignal()
}

func hasCapability(name string) bool {
	return slices.ContainsFunc(scopedModules(), func(module kernelModule) bool {
		return module.name == name
	})
}
