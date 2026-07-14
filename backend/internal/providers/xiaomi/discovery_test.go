package xiaomi

import (
	"encoding/base64"
	"encoding/binary"
	"testing"
)

func TestParseGatewayProfile(t *testing.T) {
	profile := make([]byte, 23)
	binary.BigEndian.PutUint64(profile[1:9], 123456789)
	copy(profile[9:17], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	profile[20] = 0x10
	profile[22] = 0x02
	did, group, role, mqtt := parseGatewayProfile(profile)
	if did != "123456789" || group != "0807060504030201" || role != 1 || !mqtt {
		t.Fatalf("profile decoded as did=%s group=%s role=%d mqtt=%v", did, group, role, mqtt)
	}
	encoded := base64.RawURLEncoding.EncodeToString(profile)
	decoded, err := decodeGatewayProfile(encoded)
	if err != nil || len(decoded) != len(profile) {
		t.Fatalf("decode profile: len=%d err=%v", len(decoded), err)
	}
}
