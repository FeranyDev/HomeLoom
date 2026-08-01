package source

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
)

func TestXiaomiResolverFailsClosedWithoutAuthenticatedCoreSession(t *testing.T) {
	resolver := &XiaomiResolver{}
	_, err := resolver.Resolve(context.Background(), contract.StreamSpec{SchemaVersion: 1, ID: "stream", DeviceID: "camera", Protocol: "xiaomi-miss", Profile: "main", Mode: "on_demand"})
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("resolver error = %v", err)
	}
}

func TestXiaomiResolverRejectsOtherProtocolsBeforeAuthorization(t *testing.T) {
	resolver := &XiaomiResolver{}
	_, err := resolver.Resolve(context.Background(), contract.StreamSpec{Protocol: "rtsp"})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("resolver error = %v", err)
	}
}

func TestXiaomiResolverReusesKeyWithFreshAuthorizationLeases(t *testing.T) {
	auth := &fakeAuthorizationClient{}
	resolver := &XiaomiResolver{auth: auth}
	stream := contract.StreamSpec{
		SchemaVersion: 1, ID: "stream", DeviceID: "camera", Protocol: "xiaomi-miss",
		CredentialRef: "camera-account", Profile: "main", Mode: "on_demand",
	}
	first, err := resolver.Resolve(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolver.Resolve(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	if auth.calls != 2 {
		t.Fatalf("authorization calls = %d", auth.calls)
	}
	var firstSecret, secondSecret xiaomiWorkerSecretMaterial
	if err := json.Unmarshal(first.SecretMaterial, &firstSecret); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.SecretMaterial, &secondSecret); err != nil {
		t.Fatal(err)
	}
	if len(firstSecret.ClientPublic) != 64 || len(firstSecret.ClientPrivate) != 64 ||
		firstSecret.Sign != "signature-canary" ||
		firstSecret.ClientPublic != secondSecret.ClientPublic || firstSecret.ClientPrivate != secondSecret.ClientPrivate {
		t.Fatalf("reused handshake key = first %#v second %#v", firstSecret, secondSecret)
	}
	if strings.Contains(string(auth.requests[0].ClientMaterial), firstSecret.ClientPrivate) ||
		strings.Contains(string(auth.requests[0].ClientMaterial), firstSecret.Sign) {
		t.Fatalf("private material escaped Worker: %s", auth.requests[0].ClientMaterial)
	}
	if string(auth.requests[0].ClientMaterial) != string(auth.requests[1].ClientMaterial) {
		t.Fatalf("client public key changed within one Worker: %s != %s", auth.requests[0].ClientMaterial, auth.requests[1].ClientMaterial)
	}
}

func TestXiaomiResolverSharesKeyAcrossConcurrentAuthorizationLeases(t *testing.T) {
	auth := &fakeAuthorizationClient{}
	resolver := &XiaomiResolver{auth: auth}
	stream := contract.StreamSpec{DeviceID: "camera", Protocol: "xiaomi-miss", CredentialRef: "account"}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := resolver.Resolve(context.Background(), stream)
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	if auth.calls != 8 {
		t.Fatalf("concurrent authorization calls = %d", auth.calls)
	}
	for _, request := range auth.requests[1:] {
		if string(request.ClientMaterial) != string(auth.requests[0].ClientMaterial) {
			t.Fatalf("concurrent request used another client key: %s != %s", request.ClientMaterial, auth.requests[0].ClientMaterial)
		}
	}
}

func TestXiaomiResolverReacquiresKeyAfterWorkerRestart(t *testing.T) {
	auth := &fakeAuthorizationClient{}
	stream := contract.StreamSpec{DeviceID: "camera", Protocol: "xiaomi-miss", CredentialRef: "account"}
	if _, err := (&XiaomiResolver{auth: auth}).Resolve(context.Background(), stream); err != nil {
		t.Fatal(err)
	}
	if _, err := (&XiaomiResolver{auth: auth}).Resolve(context.Background(), stream); err != nil {
		t.Fatal(err)
	}
	if auth.calls != 2 {
		t.Fatalf("authorization calls after resolver restart = %d", auth.calls)
	}
	var first, second struct {
		ClientPublic string `json:"clientPublic"`
	}
	if err := json.Unmarshal(auth.requests[0].ClientMaterial, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(auth.requests[1].ClientMaterial, &second); err != nil {
		t.Fatal(err)
	}
	if first.ClientPublic == second.ClientPublic {
		t.Fatal("worker restart reused the previous in-memory client key")
	}
}

type fakeAuthorizationClient struct {
	mu       sync.Mutex
	calls    int
	requests []contract.AuthorizationRequest
}

func (f *fakeAuthorizationClient) Acquire(_ context.Context, request contract.AuthorizationRequest) (contract.AuthorizationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	request.ClientMaterial = append([]byte(nil), request.ClientMaterial...)
	f.requests = append(f.requests, request)
	public, _ := json.Marshal(map[string]any{
		"userId": "account", "region": "cn", "did": "did", "model": "camera",
		"localIP": "192.0.2.20", "subtype": "hd", "channel": 1,
		"devicePublic": strings.Repeat("c", 64), "vendor": "cs2",
	})
	secret, _ := json.Marshal(map[string]string{"sign": "signature-canary"})
	return contract.AuthorizationResponse{
		SchemaVersion: 1, LeaseID: "lease", ExpiresAt: time.Now().Add(time.Minute),
		Endpoint: contract.EndpointSpec{Protocol: "xiaomi-miss", Host: "192.0.2.20"},
		AuthType: "vendor", PublicMaterial: public, SecretMaterial: secret, MaxUses: 1,
	}, nil
}
