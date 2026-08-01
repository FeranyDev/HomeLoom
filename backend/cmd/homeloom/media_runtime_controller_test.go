package main

import (
	"encoding/json"
	"testing"

	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
)

func TestEmbeddedStreamSpecCopiesLogicalState(t *testing.T) {
	input := domainmedia.StreamSpec{
		SchemaVersion: domainmedia.SchemaVersion, ID: "camera-main",
		DeviceID: "camera-1", Protocol: domainmedia.ProtocolRTSP,
		CredentialRef: "credential-1", Profile: "main",
		Mode: domainmedia.StreamOnDemand, Audio: true,
		Options: json.RawMessage(`{"publisher":"none"}`),
	}
	result := embeddedStreamSpec(input)
	input.Options[0] = '!'
	if result.ID != "camera-main" || result.Protocol != "rtsp" ||
		string(result.Options) != `{"publisher":"none"}` {
		t.Fatalf("embedded stream = %#v", result)
	}
}
