//go:build embed_webui

package webui

import (
	"io/fs"
	"strings"
	"testing"
)

func TestEmbeddedAssetsContainProductionIndex(t *testing.T) {
	payload, err := fs.ReadFile(Assets(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `id="root"`) || !strings.Contains(string(payload), `/assets/`) {
		t.Fatalf("embedded index.html is not the Vite production entry: %s", payload)
	}
}
