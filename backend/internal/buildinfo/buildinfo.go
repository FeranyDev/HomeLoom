package buildinfo

import "runtime"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	GoVersion string `json:"goVersion"`
}

func Current() Info {
	return Info{Version: Version, Commit: Commit, BuildTime: BuildTime, GoVersion: runtime.Version()}
}
