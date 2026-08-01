package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	mediaruntime "github.com/feranydev/homeloom/backend/internal/mediaruntime"
	"go.uber.org/zap"
)

type directMediaAuthorizer interface {
	AcquireMediaAuthorization(context.Context, domainmedia.AuthorizationRequest) (*domainmedia.AuthorizationResponse, error)
}

type embeddedMediaRuntime struct {
	runtime *mediaruntime.Runtime
	store   mediaReplayStore
	logger  *zap.Logger
	auth    directMediaAuthorizer
}

type embeddedMediaConfig struct {
	CameraKernelBinary string
	RuntimeDir         string
	HAPHost            string
	HAPPortBase        int
	RTSPPortBase       int
	SRTPPortBase       int
	ChildLogLevel      string
	ChildLogWriter     func(string, string) io.Writer
}

func newEmbeddedMediaRuntime(ctx context.Context, store mediaReplayStore, auth directMediaAuthorizer, config embeddedMediaConfig, logger *zap.Logger) (*embeddedMediaRuntime, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	logger = logger.With(zap.String("module", "media-runtime"))
	if store == nil || auth == nil {
		return nil, errors.New("embedded media runtime dependencies are required")
	}
	binary := resolveBundledMediaBinary(config.CameraKernelBinary)
	runtime, err := mediaruntime.Start(mediaruntime.Config{
		CameraKernelBinary: binary, RuntimeDir: config.RuntimeDir,
		HAPHost: config.HAPHost, HAPPort: config.HAPPortBase,
		RTSPPort: config.RTSPPortBase, SRTPPort: config.SRTPPortBase,
		ChildLogLevel: config.ChildLogLevel, ChildLogWriter: config.ChildLogWriter,
		Authorize: func(ctx context.Context, request mediaruntime.AuthorizationRequest) (mediaruntime.AuthorizationResponse, error) {
			response, err := auth.AcquireMediaAuthorization(ctx, domainmedia.AuthorizationRequest{
				SchemaVersion: domainmedia.SchemaVersion,
				RequestID:     request.RequestID, WorkerID: "local-media",
				WorkerInstanceID: "core-process", DeviceID: request.DeviceID,
				Protocol:       domainmedia.Protocol(request.Protocol),
				Purpose:        domainmedia.AuthorizationPurpose(request.Purpose),
				Attempt:        request.Attempt,
				ClientMaterial: append(json.RawMessage(nil), request.ClientMaterial...),
				SessionOffer:   append([]byte(nil), request.SessionOffer...),
			})
			if err != nil {
				return mediaruntime.AuthorizationResponse{}, err
			}
			if response == nil {
				return mediaruntime.AuthorizationResponse{}, errors.New("media authorization returned no response")
			}
			return mediaruntime.AuthorizationResponse{
				LeaseID: response.LeaseID, ExpiresAt: response.ExpiresAt,
				Endpoint: mediaruntime.Endpoint{
					Protocol: string(response.Endpoint.Protocol), Host: response.Endpoint.Host,
					Port: response.Endpoint.Port, Path: response.Endpoint.Path,
				},
				AuthType:       string(response.AuthType),
				PublicMaterial: append(json.RawMessage(nil), response.PublicMaterial...),
				SecretMaterial: append(json.RawMessage(nil), response.SecretMaterial...),
				SessionAnswer:  append([]byte(nil), response.SessionAnswer...),
				Reusable:       response.Reusable, MaxUses: response.MaxUses,
			}, nil
		},
		OnPairing: func(output mediaruntime.PairingOutput) {
			logger.Info("HomeKit camera ready", zap.String("name", output.Name), zap.String("stream_id", output.StreamID), zap.String("pin", output.PIN), zap.String("hap_address", output.HAPHost), zap.String("log_path", output.LogPath))
		},
		OnError: func(err error) {
			logger.Error("camera publisher failed", zap.Error(err))
		},
	})
	if err != nil {
		return nil, err
	}
	result := &embeddedMediaRuntime{runtime: runtime, store: store, logger: logger, auth: auth}
	if err := result.Replay(ctx); err != nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("initial media replay: %w", err)
	}
	logger.Info("embedded media runtime enabled")
	return result, nil
}

func (r *embeddedMediaRuntime) Replay(ctx context.Context) error {
	if r == nil || r.runtime == nil {
		return errors.New("embedded media runtime is unavailable")
	}
	value, err := (mediaReplayProvider{store: r.store}).MediaReplay(ctx)
	if err != nil {
		return err
	}
	replay := value.(domainmedia.StreamReplay)
	streams := make([]mediaruntime.StreamSpec, len(replay.Streams))
	for index := range replay.Streams {
		streams[index] = embeddedStreamSpec(replay.Streams[index])
	}
	return r.runtime.Replay(mediaruntime.Replay{
		SchemaVersion: replay.SchemaVersion, Generation: replay.Generation,
		Revision: replay.Revision, Streams: streams,
	})
}

func (r *embeddedMediaRuntime) PublishMediaStreamMutation(_ context.Context, mutation domainmedia.StreamMutation) error {
	if r == nil || r.runtime == nil {
		return errors.New("embedded media runtime is unavailable")
	}
	return r.runtime.Apply(mediaruntime.Mutation{
		SchemaVersion: mutation.SchemaVersion, Generation: mutation.Generation,
		Revision: mutation.Revision, Action: string(mutation.Action),
		Spec: embeddedStreamSpec(mutation.Spec),
	})
}

func (r *embeddedMediaRuntime) MediaRuntimeReady() bool {
	return r != nil && r.runtime != nil && r.runtime.Ready()
}

func (r *embeddedMediaRuntime) Close() error {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Close()
}

func embeddedStreamSpec(value domainmedia.StreamSpec) mediaruntime.StreamSpec {
	return mediaruntime.StreamSpec{
		SchemaVersion: value.SchemaVersion, ID: value.ID, DeviceID: value.DeviceID,
		Protocol: string(value.Protocol), CredentialRef: value.CredentialRef,
		Profile: value.Profile, Mode: string(value.Mode), Audio: value.Audio,
		Talkback: value.Talkback, Options: append(json.RawMessage(nil), value.Options...),
	}
}

func resolveBundledMediaBinary(binary string) string {
	if filepath.IsAbs(binary) {
		return filepath.Clean(binary)
	}
	executable, _ := os.Executable()
	if !strings.ContainsRune(binary, filepath.Separator) && executable != "" {
		candidate := filepath.Join(filepath.Dir(executable), binary)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	workingDirectory, _ := os.Getwd()
	for _, directory := range []string{
		filepath.Join(workingDirectory, "bin"),
		filepath.Join(workingDirectory, "backend", "bin"),
	} {
		candidate := filepath.Join(directory, binary)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	absolute, err := filepath.Abs(binary)
	if err == nil {
		return absolute
	}
	return binary
}
