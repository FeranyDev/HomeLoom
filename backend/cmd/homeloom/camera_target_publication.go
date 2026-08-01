package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/targets/homekit"
	mattertarget "github.com/feranydev/homeloom/backend/internal/targets/matter"
	"gopkg.in/yaml.v3"
)

const (
	maxMatterMediaResponseBytes = 2 << 20
	maxMatterSnapshotBytes      = 2 << 20
)

type cameraTargetPublication struct {
	media       *application.MediaService
	devices     *application.DeviceService
	runtimeDir  string
	hapPortBase int

	mu      sync.Mutex
	owners  map[string]string
	streams map[string]string
}

func newCameraTargetPublication(media *application.MediaService, devices *application.DeviceService, runtimeDir string, hapPortBase int) *cameraTargetPublication {
	return &cameraTargetPublication{
		media: media, devices: devices, runtimeDir: runtimeDir, hapPortBase: hapPortBase,
		owners: make(map[string]string), streams: make(map[string]string),
	}
}

func (p *cameraTargetPublication) EnableHomeKitCamera(ctx context.Context, targetID, deviceID, name string) (homekit.PairingInfo, bool, string, error) {
	if p == nil || p.media == nil {
		return homekit.PairingInfo{}, false, "", errors.New("media service is unavailable")
	}
	stream, err := p.streamForDevice(ctx, deviceID)
	if err != nil {
		return homekit.PairingInfo{}, false, "", err
	}
	p.mu.Lock()
	if owner := p.owners[stream.ID]; owner != "" && owner != targetID {
		p.mu.Unlock()
		return homekit.PairingInfo{}, false, "", fmt.Errorf("camera %q is already published by target %q", deviceID, owner)
	}
	p.owners[stream.ID], p.streams[targetID] = targetID, stream.ID
	p.mu.Unlock()

	stream.Options = json.RawMessage(`{"publisher":"apple-home"}`)
	if _, err := p.media.Update(ctx, stream.ID, stream); err != nil {
		p.release(targetID, stream.ID)
		return homekit.PairingInfo{}, false, "", fmt.Errorf("enable HomeKit Camera stream: %w", err)
	}
	if runtimeErr := p.media.LastRuntimeError(); runtimeErr != nil {
		stream.Options = json.RawMessage(`{"publisher":"none"}`)
		_, _ = p.media.Update(ctx, stream.ID, stream)
		p.reportAvailability(deviceID, device.AvailabilityOffline)
		p.release(targetID, stream.ID)
		return homekit.PairingInfo{}, false, "", fmt.Errorf("start HomeKit Camera publisher: %w", runtimeErr)
	}
	p.reportAvailability(deviceID, device.AvailabilityOnline)
	pairing, paired, address := p.waitForIdentity(ctx, stream.ID, deviceID, name)
	return pairing, paired, address, nil
}

func (p *cameraTargetPublication) reportAvailability(deviceID string, availability device.Availability) {
	if p.devices != nil {
		_, _ = p.devices.ReportDeviceAvailability(deviceID, availability)
	}
}

func (p *cameraTargetPublication) DisableHomeKitCamera(ctx context.Context, targetID, deviceID string) error {
	if p == nil || p.media == nil {
		return nil
	}
	p.mu.Lock()
	streamID := p.streams[targetID]
	if streamID == "" {
		p.mu.Unlock()
		return nil
	}
	if p.owners[streamID] != targetID {
		delete(p.streams, targetID)
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	streams, err := p.media.List(ctx)
	if err != nil {
		return err
	}
	for _, stream := range streams {
		if stream.ID != streamID || stream.DeviceID != deviceID {
			continue
		}
		stream.Options = json.RawMessage(`{"publisher":"none"}`)
		if _, err := p.media.Update(ctx, stream.ID, stream); err != nil {
			return fmt.Errorf("disable HomeKit Camera stream: %w", err)
		}
		break
	}
	p.release(targetID, streamID)
	return nil
}

func (p *cameraTargetPublication) InspectHomeKitCamera(deviceID string) (homekit.PairingInfo, bool, string) {
	streamID := defaultCameraStreamID(deviceID)
	pairing, paired, address, _ := p.readIdentity(streamID, deviceID, deviceID)
	return pairing, paired, address
}

func (p *cameraTargetPublication) ResetHomeKitCamera(ctx context.Context, targetID, deviceID string) error {
	if err := p.DisableHomeKitCamera(ctx, targetID, deviceID); err != nil {
		return err
	}
	streamID := defaultCameraStreamID(deviceID)
	directory := filepath.Join(p.runtimeDir, streamID)
	if filepath.Clean(directory) == filepath.Clean(p.runtimeDir) {
		return errors.New("unsafe HomeKit Camera identity path")
	}
	return os.RemoveAll(directory)
}

func (p *cameraTargetPublication) WebRTC(
	ctx context.Context,
	streamID string,
	request mattertarget.CameraWebRTCRequest,
) (mattertarget.CameraWebRTCResponse, error) {
	var response mattertarget.CameraWebRTCResponse
	if p == nil {
		return response, errors.New("camera media runtime is unavailable")
	}
	payload := struct {
		Operation string                           `json:"operation"`
		SessionID string                           `json:"sessionId,omitempty"`
		StreamID  string                           `json:"streamId,omitempty"`
		SDP       string                           `json:"sdp,omitempty"`
		Candidate *mattertarget.CameraICECandidate `json:"candidate,omitempty"`
	}{
		Operation: request.Operation,
		SessionID: request.SessionID,
		SDP:       request.SDP,
		Candidate: request.Candidate,
	}
	if request.Operation == "open" {
		payload.StreamID = streamID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return response, err
	}
	httpResponse, err := p.mediaRequest(ctx, streamID, http.MethodPost, "/api/matter/webrtc", body)
	if err != nil {
		return response, err
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		return response, fmt.Errorf("camera WebRTC backend returned status %d", httpResponse.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(httpResponse.Body, maxMatterMediaResponseBytes+1))
	if err := decoder.Decode(&response); err != nil {
		return response, errors.New("decode camera WebRTC response")
	}
	if response.SessionID == "" && request.Operation != "close" {
		return response, errors.New("camera WebRTC response is incomplete")
	}
	return response, nil
}

func (p *cameraTargetPublication) Snapshot(
	ctx context.Context,
	streamID string,
	width uint16,
	height uint16,
) ([]byte, error) {
	if p == nil {
		return nil, errors.New("camera media runtime is unavailable")
	}
	query := url.Values{
		"src":    []string{streamID},
		"width":  []string{strconv.Itoa(int(width))},
		"height": []string{strconv.Itoa(int(height))},
	}
	response, err := p.mediaRequest(ctx, streamID, http.MethodGet, "/api/frame.jpeg?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("camera snapshot backend returned status %d", response.StatusCode)
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "image/jpeg") {
		return nil, errors.New("camera snapshot backend returned an invalid content type")
	}
	jpeg, err := io.ReadAll(io.LimitReader(response.Body, maxMatterSnapshotBytes+1))
	if err != nil {
		return nil, errors.New("read camera snapshot response")
	}
	if len(jpeg) == 0 || len(jpeg) > maxMatterSnapshotBytes ||
		len(jpeg) < 4 || jpeg[0] != 0xff || jpeg[1] != 0xd8 ||
		jpeg[len(jpeg)-2] != 0xff || jpeg[len(jpeg)-1] != 0xd9 {
		return nil, errors.New("camera snapshot backend returned invalid JPEG data")
	}
	return jpeg, nil
}

func (p *cameraTargetPublication) mediaRequest(
	ctx context.Context,
	streamID string,
	method string,
	requestPath string,
	body []byte,
) (*http.Response, error) {
	if !validCameraStreamID(streamID) {
		return nil, errors.New("invalid camera media stream")
	}
	socketPath := filepath.Join(p.runtimeDir, streamID, "media.sock")
	if filepath.Dir(filepath.Dir(socketPath)) != filepath.Clean(p.runtimeDir) {
		return nil, errors.New("invalid camera media socket")
	}
	transport := &http.Transport{
		DialContext: func(dialContext context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(dialContext, "unix", socketPath)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://camera.local"+requestPath, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("User-Agent", "HomeLoom-Matter-Camera/1")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return nil, errors.New("camera media backend is unavailable")
	}
	return response, nil
}

func validCameraStreamID(streamID string) bool {
	if len(streamID) < len("camera-x") || len(streamID) > 128 || !strings.HasPrefix(streamID, "camera-") {
		return false
	}
	for _, character := range streamID {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func (p *cameraTargetPublication) streamForDevice(ctx context.Context, deviceID string) (domainmedia.StreamSpec, error) {
	streams, err := p.media.List(ctx)
	if err != nil {
		return domainmedia.StreamSpec{}, err
	}
	for _, stream := range streams {
		if stream.DeviceID == deviceID {
			return stream, nil
		}
	}
	return domainmedia.StreamSpec{}, fmt.Errorf("camera %q has no media stream", deviceID)
}

func (p *cameraTargetPublication) waitForIdentity(ctx context.Context, streamID, deviceID, name string) (homekit.PairingInfo, bool, string) {
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if pairing, paired, address, found := p.readIdentity(streamID, deviceID, name); found {
			return pairing, paired, address
		}
		select {
		case <-ctx.Done():
			return homekit.PairingInfo{Devices: []string{deviceID}}, false, p.address(streamID)
		case <-deadline.C:
			return homekit.PairingInfo{Devices: []string{deviceID}}, false, p.address(streamID)
		case <-ticker.C:
		}
	}
}

func (p *cameraTargetPublication) readIdentity(streamID, deviceID, _ string) (homekit.PairingInfo, bool, string, bool) {
	raw, err := os.ReadFile(filepath.Join(p.runtimeDir, streamID, "homekit-identity.json"))
	if err != nil {
		return homekit.PairingInfo{}, false, p.address(streamID), false
	}
	var identity struct {
		PIN string `json:"pin"`
	}
	if json.Unmarshal(raw, &identity) != nil || identity.PIN == "" {
		return homekit.PairingInfo{}, false, p.address(streamID), false
	}
	paired := false
	if raw, readErr := os.ReadFile(filepath.Join(p.runtimeDir, streamID, "homekit-pairings.json")); readErr == nil {
		var document struct {
			SchemaVersion int      `json:"schemaVersion"`
			Pairings      []string `json:"pairings"`
		}
		if json.Unmarshal(raw, &document) == nil && document.SchemaVersion == 1 {
			paired = len(document.Pairings) > 0
		}
	}
	if !paired {
		if config, readErr := os.ReadFile(filepath.Join(p.runtimeDir, streamID, "go2rtc.yaml")); readErr == nil {
			var document struct {
				HomeKit map[string]struct {
					Pairings []string `yaml:"pairings"`
				} `yaml:"homekit"`
			}
			if yaml.Unmarshal(config, &document) == nil {
				paired = len(document.HomeKit[streamID].Pairings) > 0
			}
		}
	}
	return homekit.PairingInfo{Code: identity.PIN, Devices: []string{deviceID}}, paired, p.address(streamID), true
}

func (p *cameraTargetPublication) address(streamID string) string {
	if raw, err := os.ReadFile(filepath.Join(p.runtimeDir, streamID, "publisher-endpoint.json")); err == nil {
		var endpoint struct {
			SchemaVersion int `json:"schemaVersion"`
			HAPPort       int `json:"hapPort"`
		}
		if json.Unmarshal(raw, &endpoint) == nil && endpoint.SchemaVersion == 1 &&
			endpoint.HAPPort >= 1024 && endpoint.HAPPort <= 65535 {
			return fmt.Sprintf(":%d", endpoint.HAPPort)
		}
	}
	digest := sha256.Sum256([]byte(streamID))
	offset := (int(digest[0])<<8 | int(digest[1])) % 1000
	return fmt.Sprintf(":%d", p.hapPortBase+offset)
}

func (p *cameraTargetPublication) release(targetID, streamID string) {
	p.mu.Lock()
	if p.owners[streamID] == targetID {
		delete(p.owners, streamID)
	}
	delete(p.streams, targetID)
	p.mu.Unlock()
}
