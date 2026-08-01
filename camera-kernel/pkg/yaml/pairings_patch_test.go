package yaml_test

import (
        "strings"
        "testing"

        "github.com/AlexxIT/go2rtc/pkg/yaml"
)

func TestPatchQuotedHomeKitStreamPairings(t *testing.T) {
        src := []byte(`app:
  modules: [api]
homekit:
  "camera-c616509e49787d37":
    name: "xiaomi"
    pin: "111-22-333"
    device_id: "AA:BB:CC:DD:EE:FF"
    pairings: []
`)
        out, err := yaml.Patch(src, []string{"homekit", "camera-c616509e49787d37", "pairings"}, []string{"client_id=x&client_public=ab&permissions=1"})
        if err != nil {
                t.Fatal(err)
        }
        text := string(out)
        if !strings.Contains(text, "client_id=x&client_public=ab&permissions=1") {
                t.Fatalf("pairings not patched:\n%s", text)
        }
        if strings.Contains(text, "pairings: []") {
                t.Fatalf("empty pairings remained:\n%s", text)
        }
}

func TestPatchEmptyPairingsListNode(t *testing.T) {
        src := []byte("homekit:\n  camera1:\n    pairings: []\n")
        out, err := yaml.Patch(src, []string{"homekit", "camera1", "pairings"}, []string{"client_id=x"})
        if err != nil {
                t.Fatal(err)
        }
        if !strings.Contains(string(out), "- client_id=x") {
                t.Fatalf("unexpected patch result:\n%s", out)
        }
}
