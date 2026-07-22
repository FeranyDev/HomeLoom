//go:build !embed_webui

package webui

import "io/fs"

// Assets is nil in ordinary development builds. The Vite development server
// remains responsible for the UI unless the embed_webui build tag is enabled.
func Assets() fs.FS { return nil }
