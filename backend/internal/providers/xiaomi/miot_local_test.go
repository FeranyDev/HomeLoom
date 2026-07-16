package xiaomi

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestMIoTLocalPacketRoundTripAndChecksumValidation(t *testing.T) {
	token := []byte("0123456789abcdef")
	deviceID := [4]byte{0x12, 0x34, 0x56, 0x78}
	payload := []byte(`{"id":1,"method":"get_properties","params":[]}` + "\x00")
	packet, err := buildMIoTPacket(deviceID, 1_700_000_000, token, payload)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := parseMIoTPacket(packet, token)
	if err != nil || !bytes.Equal(decoded, bytes.TrimRight(payload, "\x00")) {
		t.Fatalf("decoded = %q, error = %v", decoded, err)
	}
	packet[len(packet)-1] ^= 0xff
	if _, err := parseMIoTPacket(packet, token); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered packet error = %v", err)
	}
}

func TestMIoTLocalHandshakeUsesDeviceIDAndTimestampFields(t *testing.T) {
	response := append([]byte(nil), miotHelloPacket...)
	copy(response[8:12], []byte{1, 2, 3, 4})
	binary.BigEndian.PutUint32(response[12:16], 123456)
	deviceID, timestamp, err := parseMIoTHandshake(response)
	if err != nil || deviceID != [4]byte{1, 2, 3, 4} || timestamp != 123456 {
		t.Fatalf("deviceID=%x timestamp=%d error=%v", deviceID, timestamp, err)
	}
}

func TestMIoTLocalAccessRequiresPrivateAddressAndSixteenByteToken(t *testing.T) {
	if !validLocalAccess("192.168.1.20", "30313233343536373839616263646566") {
		t.Fatal("expected private IPv4 and hex token to be accepted")
	}
	if validLocalAccess("8.8.8.8", "30313233343536373839616263646566") || validLocalAccess("192.168.1.20", "short") {
		t.Fatal("public IP or invalid token was accepted")
	}
}

func TestHubDeviceNeverSerializesLocalToken(t *testing.T) {
	encoded, err := json.Marshal(HubDevice{DID: "1", Name: "Device", LocalIP: "192.168.1.2", Local: true, Token: "secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-token") || !strings.Contains(string(encoded), `"localAvailable":true`) {
		t.Fatalf("encoded device = %s", encoded)
	}
}
