package streams

import (
	"net/url"
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
