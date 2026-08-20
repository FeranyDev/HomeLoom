package streams

import (
	"net/url"
	"strconv"
	"strings"
)

func ParseQuery(s string) url.Values {
	if len(s) == 0 {
		return nil
	}
	params := url.Values{}
	for _, key := range strings.Split(s, "#") {
		var value string
		i := strings.IndexByte(key, '=')
		if i > 0 {
			key, value = key[:i], key[i+1:]
		}
		params[key] = append(params[key], value)
	}
	return params
}

// hardwareRetryLimit returns the number of transient producer restarts that a
// hardware source may survive before it is demoted to software. A configured
// limit is useful for live HEVC sources that occasionally begin mid-GOP: the
// hardware device is healthy, but the first decoder instance cannot yet emit
// a complete frame. Sources without the marker preserve the historical
// immediate-demotion behavior.
func hardwareRetryLimit(source string) int {
	_, rawQuery, hasQuery := strings.Cut(source, "#")
	if !hasQuery {
		return 0
	}
	for _, part := range strings.Split(rawQuery, "#") {
		key, value, _ := strings.Cut(part, "=")
		if key != "hardware_retry" {
			continue
		}
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 {
			return 0
		}
		return limit
	}
	return 0
}

func shouldDemoteHardware(source string, retry int) bool {
	return retry >= hardwareRetryLimit(source)
}

// demoteHardwareURL strips hardware acceleration selectors so reconnect can
// fall back to the software encoder after a hardware path fails mid-stream.
func demoteHardwareURL(source string) string {
	base, rawQuery, hasQuery := strings.Cut(source, "#")
	if !hasQuery {
		return source
	}
	parts := strings.Split(rawQuery, "#")
	kept := make([]string, 0, len(parts))
	changed := false
	for _, part := range parts {
		key, value, _ := strings.Cut(part, "=")
		switch {
		case key == "hardware":
			changed = true
			continue
		case key == "video" && (strings.Contains(value, "/") || value == "h264_videotoolbox" || value == "h265_videotoolbox"):
			// h264/videotoolbox, h264/vaapi, ... -> software h264
			if i := strings.IndexByte(value, '/'); i > 0 {
				kept = append(kept, "video="+value[:i])
			} else {
				kept = append(kept, "video=h264")
			}
			changed = true
		default:
			kept = append(kept, part)
		}
	}
	if !changed {
		return source
	}
	if len(kept) == 0 {
		return base
	}
	return base + "#" + strings.Join(kept, "#")
}
