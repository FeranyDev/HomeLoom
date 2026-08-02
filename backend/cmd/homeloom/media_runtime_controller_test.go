package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"go.uber.org/zap"
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

func TestNewEmbeddedMediaRuntimeDefersUnavailableProviderReplay(t *testing.T) {
	want := providersdk.ErrProviderUnavailable
	authorizer := &mediaRuntimeAuthorizerStub{err: want}
	result, err := newEmbeddedMediaRuntime(context.Background(), mediaReplayStoreStub{
		version: gormstore.MediaConfigVersion{Generation: 1, Revision: 1},
		streams: []gormstore.MediaStream{{
			ID: "stream-front", DeviceID: "camera-front", Protocol: "rtsp",
			Profile: "main", Mode: "on_demand", Enabled: true,
		}},
	}, authorizer, embeddedMediaConfig{
		CameraKernelBinary: "/missing/homeloom-camera-kernel",
		RuntimeDir:         t.TempDir(), HAPHost: "127.0.0.1",
		HAPPortBase: 12000, RTSPPortBase: 13000, SRTPPortBase: 14000,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("newEmbeddedMediaRuntime() error = %v", err)
	}
	if result == nil || result.MediaRuntimeReady() {
		t.Fatalf("deferred runtime = %#v, ready=%v", result, result != nil && result.MediaRuntimeReady())
	}
	if authorizer.calls != 1 {
		t.Fatalf("initial authorization calls = %d, want 1", authorizer.calls)
	}
	mutation := domainmedia.StreamMutation{
		SchemaVersion: domainmedia.SchemaVersion, Generation: 1, Revision: 2,
		Action: domainmedia.MutationUpsert,
		Spec: domainmedia.StreamSpec{
			SchemaVersion: domainmedia.SchemaVersion, ID: "stream-front", DeviceID: "camera-front",
			Protocol: domainmedia.ProtocolRTSP, Profile: "main", Mode: domainmedia.StreamOnDemand,
			Options: json.RawMessage(`{"publisher":"apple-home"}`),
		},
	}
	if err := result.PublishMediaStreamMutation(context.Background(), mutation); err != nil {
		t.Fatalf("deferred mutation = %v", err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("deferred mutation authorization calls = %d, want 1", authorizer.calls)
	}
	if err := result.Replay(context.Background()); !errors.Is(err, want) {
		t.Fatalf("retry replay error = %v, want provider unavailable", err)
	}
	if authorizer.calls != 2 {
		t.Fatalf("retry authorization calls = %d, want 2", authorizer.calls)
	}
	if result.MediaRuntimeReady() {
		t.Fatal("deferred runtime reported ready after recoverable replay failure")
	}
	if err := result.Close(); err != nil {
		t.Fatalf("close deferred runtime: %v", err)
	}
}

func TestNewEmbeddedMediaRuntimeEnablesAfterNormalReplay(t *testing.T) {
	result, err := newEmbeddedMediaRuntime(context.Background(), mediaReplayStoreStub{
		version: gormstore.MediaConfigVersion{Generation: 1, Revision: 1},
	}, &mediaRuntimeAuthorizerStub{}, embeddedMediaConfig{
		CameraKernelBinary: "/missing/homeloom-camera-kernel",
		RuntimeDir:         t.TempDir(), HAPHost: "127.0.0.1",
		HAPPortBase: 12000, RTSPPortBase: 13000, SRTPPortBase: 14000,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("newEmbeddedMediaRuntime() error = %v", err)
	}
	if result == nil || !result.MediaRuntimeReady() {
		t.Fatalf("normal runtime = %#v, ready=%v", result, result != nil && result.MediaRuntimeReady())
	}
	if err := result.Close(); err != nil {
		t.Fatalf("close normal runtime: %v", err)
	}
}

func TestDeferredEmbeddedMediaRuntimeRecoversOnSuccessfulReplay(t *testing.T) {
	store := &recoveringMediaReplayStore{streams: []gormstore.MediaStream{{
		ID: "stream-front", DeviceID: "camera-front", Protocol: "rtsp",
		Profile: "main", Mode: "on_demand", Enabled: true,
	}}}
	authorizer := &mediaRuntimeAuthorizerStub{err: providersdk.ErrProviderUnavailable}
	result, err := newEmbeddedMediaRuntime(context.Background(), store, authorizer, embeddedMediaConfig{
		CameraKernelBinary: "/missing/homeloom-camera-kernel",
		RuntimeDir:         t.TempDir(), HAPHost: "127.0.0.1",
		HAPPortBase: 12000, RTSPPortBase: 13000, SRTPPortBase: 14000,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("newEmbeddedMediaRuntime() error = %v", err)
	}
	if result.MediaRuntimeReady() {
		t.Fatal("deferred runtime reported ready")
	}
	authorizer.err = nil
	store.mu.Lock()
	store.streams = nil
	store.mu.Unlock()
	if err := result.Replay(context.Background()); err != nil {
		t.Fatalf("successful recovery replay = %v", err)
	}
	if !result.MediaRuntimeReady() {
		t.Fatal("runtime did not become ready after successful replay")
	}
	if err := result.Close(); err != nil {
		t.Fatalf("close recovered runtime: %v", err)
	}
}

func TestEmbeddedMediaRuntimeDeferredMutationStillValidatesContract(t *testing.T) {
	result, err := newEmbeddedMediaRuntime(context.Background(), mediaReplayStoreStub{
		version: gormstore.MediaConfigVersion{Generation: 1, Revision: 1},
		streams: []gormstore.MediaStream{{
			ID: "stream-front", DeviceID: "camera-front", Protocol: "rtsp",
			Profile: "main", Mode: "on_demand", Enabled: true,
		}},
	}, &mediaRuntimeAuthorizerStub{err: providersdk.ErrProviderUnavailable}, embeddedMediaConfig{
		CameraKernelBinary: "/missing/homeloom-camera-kernel",
		RuntimeDir:         t.TempDir(), HAPHost: "127.0.0.1",
		HAPPortBase: 12000, RTSPPortBase: 13000, SRTPPortBase: 14000,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("newEmbeddedMediaRuntime() error = %v", err)
	}
	invalid := domainmedia.StreamMutation{SchemaVersion: domainmedia.SchemaVersion, Generation: 1, Revision: 2, Action: domainmedia.MutationUpsert}
	if err := result.PublishMediaStreamMutation(context.Background(), invalid); err == nil {
		t.Fatal("deferred runtime accepted invalid mutation")
	}
	_ = result.Close()
}

func TestNewEmbeddedMediaRuntimeKeepsStructuralReplayErrorsFatal(t *testing.T) {
	want := errors.New("invalid source configuration")
	result, err := newEmbeddedMediaRuntime(context.Background(), mediaReplayStoreStub{
		version: gormstore.MediaConfigVersion{Generation: 1, Revision: 1},
		streams: []gormstore.MediaStream{{
			ID: "stream-front", DeviceID: "camera-front", Protocol: "rtsp",
			Profile: "main", Mode: "on_demand", Enabled: true,
		}},
	}, &mediaRuntimeAuthorizerStub{err: want}, embeddedMediaConfig{
		CameraKernelBinary: "/missing/homeloom-camera-kernel",
		RuntimeDir:         t.TempDir(), HAPHost: "127.0.0.1",
		HAPPortBase: 12000, RTSPPortBase: 13000, SRTPPortBase: 14000,
	}, zap.NewNop())
	if result != nil {
		t.Fatalf("structural replay returned runtime: %#v", result)
	}
	if !errors.Is(err, want) {
		t.Fatalf("structural replay error = %v, want cause %v", err, want)
	}
}

type mediaRuntimeAuthorizerStub struct {
	err   error
	calls int
}

func (a *mediaRuntimeAuthorizerStub) AcquireMediaAuthorization(context.Context, domainmedia.AuthorizationRequest) (*domainmedia.AuthorizationResponse, error) {
	a.calls++
	if a.err != nil {
		return nil, a.err
	}
	return &domainmedia.AuthorizationResponse{}, nil
}

type recoveringMediaReplayStore struct {
	mu      sync.Mutex
	streams []gormstore.MediaStream
}

func (s *recoveringMediaReplayStore) ListMediaStreams(context.Context, string) ([]gormstore.MediaStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]gormstore.MediaStream(nil), s.streams...), nil
}

func (*recoveringMediaReplayStore) GetMediaConfigVersion(context.Context) (gormstore.MediaConfigVersion, error) {
	return gormstore.MediaConfigVersion{Generation: 1, Revision: 1}, nil
}
