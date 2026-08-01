package homekit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/rtp"
	"github.com/brutella/hap/service"
	"github.com/brutella/hap/tlv8"
	homekitqr "github.com/kradalby/homekit-qr"
	"go.uber.org/zap"
)

const (
	cameraOperationTimeout = 15 * time.Second
	maxSnapshotRequestBody = 8 << 10
	maxSnapshotDimension   = 4096
	maxSnapshotBytes       = 16 << 20
)

// CameraMedia is the Core-to-Media-Worker boundary used by the HomeKit
// consumer. Implementations must keep SRTP keys in memory only and must make
// End commands idempotent.
type CameraMedia interface {
	Snapshot(context.Context, string, int, int) ([]byte, error)
	PrepareStream(context.Context, string, rtp.SetupEndpoints) (rtp.SetupEndpointsResponse, error)
	SetStream(context.Context, string, rtp.StreamConfiguration) error
}

// CameraPublisherConfig describes one standalone HomeKit IP Camera publisher.
// Camera publishers deliberately do not share the ordinary HomeLoom bridge
// identity: each camera has its own address, PIN, setup ID, mDNS accessory and
// persistent pairing store.
type CameraPublisherConfig struct {
	ID        string
	DeviceID  string
	Name      string
	Address   string
	Pin       string
	SetupID   string
	StorePath string
	Store     hap.Store
}

type CameraPublisher struct {
	id       string
	deviceID string
	server   *hap.Server
	camera   *accessory.Camera
	pairing  PairingInfo
	logger   *zap.Logger
}

func NewCameraPublisher(config CameraPublisherConfig, media CameraMedia, logger *zap.Logger) (*CameraPublisher, error) {
	if config.ID == "" || config.DeviceID == "" {
		return nil, errors.New("HomeKit camera publisher and device IDs are required")
	}
	if config.Name == "" || config.Address == "" || len(config.Pin) != 8 || len(config.SetupID) != 4 {
		return nil, errors.New("HomeKit camera name, address, eight-digit PIN, and four-character setup ID are required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	logger = logger.With(zap.String("module", "homekit-camera"))
	store := config.Store
	if store == nil {
		if config.StorePath == "" {
			return nil, errors.New("HomeKit camera pairing store is required")
		}
		created, err := newSecureFSStore(config.StorePath)
		if err != nil {
			return nil, err
		}
		store = created
	}
	camera, err := newCameraAccessory(accessory.Info{
		Name: config.Name, SerialNumber: config.DeviceID,
		Manufacturer: "HomeLoom", Model: "HomeLoom Camera", Firmware: "0.0.1",
	}, 1, config.DeviceID, media)
	if err != nil {
		return nil, err
	}
	server, err := hap.NewServer(store, camera.A)
	if err != nil {
		return nil, fmt.Errorf("create HomeKit camera HAP server: %w", err)
	}
	server.Addr = config.Address
	server.Pin = config.Pin
	server.SetupId = config.SetupID
	server.ServeMux().HandleFunc("/resource", newCameraSnapshotHandler(
		server.IsAuthorized,
		media,
		map[uint64]string{camera.A.Id: config.DeviceID},
	))

	qrConfig := homekitqr.QRCodeConfig{SetupURIConfig: homekitqr.SetupURIConfig{
		Category: homekitqr.CategoryIPCamera, Flag: 2,
		PairingCode: config.Pin, SetupID: config.SetupID,
	}, Size: 320}
	setupURI, err := homekitqr.ComposeSetupURI(qrConfig.SetupURIConfig)
	if err != nil {
		return nil, fmt.Errorf("compose HomeKit camera setup URI: %w", err)
	}
	qr, err := homekitqr.GenerateQRPNG(qrConfig)
	if err != nil {
		return nil, fmt.Errorf("generate HomeKit camera QR code: %w", err)
	}
	return &CameraPublisher{
		id: config.ID, deviceID: config.DeviceID,
		server: server, camera: camera, logger: logger,
		pairing: PairingInfo{
			Code: formatPin(config.Pin), SetupID: config.SetupID, SetupURI: setupURI, QR: qr,
			Devices: []string{config.DeviceID},
		},
	}, nil
}

func (p *CameraPublisher) ID() string               { return p.id }
func (p *CameraPublisher) PairingInfo() PairingInfo { return p.pairing }
func (p *CameraPublisher) IsPaired() bool           { return p.server.IsPaired() }

func (p *CameraPublisher) Start(ctx context.Context) error {
	p.logger.Info(
		"standalone HomeKit camera publisher started",
		zap.String("publisher_id", p.id),
		zap.String("device_id", p.deviceID),
		zap.String("address", p.server.Addr),
	)
	err := p.server.ListenAndServe(ctx)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

type cameraBinding struct {
	deviceID string
	camera   *accessory.Camera
	media    CameraMedia
}

func newCameraAccessory(info accessory.Info, aid uint64, deviceID string, media CameraMedia) (*accessory.Camera, error) {
	camera := accessory.NewCamera(info)
	camera.A.Id = aid
	binding := &cameraBinding{deviceID: deviceID, camera: camera, media: media}
	if err := binding.configureStreamManagement(camera.StreamManagement1); err != nil {
		return nil, err
	}
	// The upstream constructor allocates a second management service but does
	// not attach it. HAP cameras require at least two independently negotiated
	// RTP sessions.
	camera.A.AddS(camera.StreamManagement2.S)
	if err := binding.configureStreamManagement(camera.StreamManagement2); err != nil {
		return nil, err
	}
	return camera, nil
}

func (b *cameraBinding) configureStreamManagement(management *service.CameraRTPStreamManagement) error {
	video, err := tlv8.Marshal(rtp.DefaultVideoStreamConfiguration())
	if err != nil {
		return fmt.Errorf("encode HomeKit supported video configuration: %w", err)
	}
	audio, err := tlv8.Marshal(rtp.DefaultAudioStreamConfiguration())
	if err != nil {
		return fmt.Errorf("encode HomeKit supported audio configuration: %w", err)
	}
	crypto, err := tlv8.Marshal(rtp.NewConfiguration(rtp.CryptoSuite_AES_CM_128_HMAC_SHA1_80))
	if err != nil {
		return fmt.Errorf("encode HomeKit supported RTP configuration: %w", err)
	}
	management.SupportedVideoStreamConfiguration.SetValue(video)
	management.SupportedAudioStreamConfiguration.SetValue(audio)
	management.SupportedRTPConfiguration.SetValue(crypto)
	initialStatus := rtp.StreamingStatusUnavailable
	if b.media != nil {
		initialStatus = rtp.StreamingStatusAvailable
	}
	b.setStreamingStatus(management, initialStatus)

	// SetupEndpoints requires a write response containing the accessory SRTP
	// endpoint. The upstream generated characteristic omits "wr", so add it
	// explicitly and return the base64-encoded TLV through the standard HAP
	// write-response hook.
	management.SetupEndpoints.Permissions = appendPermission(
		management.SetupEndpoints.Permissions,
		characteristic.PermissionWriteResponse,
	)
	management.SetupEndpoints.SetValueRequestFunc = func(value any, request *http.Request) (any, int) {
		if b.media == nil {
			return nil, hap.JsonStatusResourceBusy
		}
		encoded, ok := value.(string)
		if !ok {
			return nil, hap.JsonStatusInvalidValueInRequest
		}
		payload, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, hap.JsonStatusInvalidValueInRequest
		}
		var setup rtp.SetupEndpoints
		if err := tlv8.Unmarshal(payload, &setup); err != nil || validateSetupEndpoints(setup) != nil {
			return nil, hap.JsonStatusInvalidValueInRequest
		}
		ctx, cancel := cameraContext(request)
		defer cancel()
		response, err := b.media.PrepareStream(ctx, b.deviceID, setup)
		if err != nil {
			return nil, hap.JsonStatusServiceCommunicationFailure
		}
		response.SessionId = append([]byte(nil), setup.SessionId...)
		if err := validateSetupEndpointsResponse(response); err != nil {
			return nil, hap.JsonStatusServiceCommunicationFailure
		}
		payload, err = tlv8.Marshal(response)
		if err != nil {
			return nil, hap.JsonStatusServiceCommunicationFailure
		}
		return base64.StdEncoding.EncodeToString(payload), hap.JsonStatusSuccess
	}
	management.SelectedRTPStreamConfiguration.SetValueRequestFunc = func(value any, request *http.Request) (any, int) {
		if b.media == nil {
			return nil, hap.JsonStatusResourceBusy
		}
		encoded, ok := value.(string)
		if !ok {
			return nil, hap.JsonStatusInvalidValueInRequest
		}
		payload, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, hap.JsonStatusInvalidValueInRequest
		}
		var selected rtp.StreamConfiguration
		if err := tlv8.Unmarshal(payload, &selected); err != nil || !validStreamCommand(selected.Command) {
			return nil, hap.JsonStatusInvalidValueInRequest
		}
		ctx, cancel := cameraContext(request)
		defer cancel()
		if err := b.media.SetStream(ctx, b.deviceID, selected); err != nil {
			return nil, hap.JsonStatusServiceCommunicationFailure
		}
		status := rtp.StreamingStatusBusy
		if selected.Command.Type == rtp.SessionControlCommandTypeEnd {
			status = rtp.StreamingStatusAvailable
		}
		b.setStreamingStatus(management, status)
		return nil, hap.JsonStatusSuccess
	}
	return nil
}

func validateSetupEndpoints(item rtp.SetupEndpoints) error {
	if len(item.SessionId) != 16 || !validRTPAddr(item.ControllerAddr) {
		return errors.New("invalid HomeKit RTP session or controller address")
	}
	if !validCryptoSuite(item.Video) || !validCryptoSuite(item.Audio) {
		return errors.New("invalid HomeKit SRTP crypto material")
	}
	return nil
}

func validateSetupEndpointsResponse(item rtp.SetupEndpointsResponse) error {
	if len(item.SessionId) != 16 || item.Status > rtp.SessionStatusError {
		return errors.New("invalid HomeKit RTP setup response")
	}
	if item.Status != rtp.SessionStatusSuccess {
		return nil
	}
	if !validRTPAddr(item.AccessoryAddr) ||
		!validCryptoSuite(item.Video) ||
		!validCryptoSuite(item.Audio) {
		return errors.New("invalid HomeKit accessory RTP endpoint")
	}
	return nil
}

func validRTPAddr(item rtp.Addr) bool {
	if item.IPVersion != rtp.IPAddrVersionv4 && item.IPVersion != rtp.IPAddrVersionv6 {
		return false
	}
	parsed := net.ParseIP(item.IPAddr)
	if parsed == nil || item.VideoRtpPort == 0 || item.AudioRtpPort == 0 {
		return false
	}
	if item.IPVersion == rtp.IPAddrVersionv4 {
		return parsed.To4() != nil
	}
	return parsed.To4() == nil
}

func validCryptoSuite(item rtp.CryptoSuite) bool {
	return item.Type == rtp.CryptoSuite_AES_CM_128_HMAC_SHA1_80 &&
		len(item.MasterKey) == 16 &&
		len(item.MasterSalt) == 14
}

func validStreamCommand(item rtp.SessionControlCommand) bool {
	if len(item.Identifier) != 16 {
		return false
	}
	return item.Type >= rtp.SessionControlCommandTypeEnd &&
		item.Type <= rtp.SessionControlCommandTypeReconfigure
}

func (b *cameraBinding) setStreamingStatus(management *service.CameraRTPStreamManagement, status byte) {
	payload, err := tlv8.Marshal(rtp.StreamingStatus{Status: status})
	if err == nil {
		management.StreamingStatus.SetValue(payload)
	}
}

func appendPermission(items []string, permission string) []string {
	for _, item := range items {
		if item == permission {
			return items
		}
	}
	return append(items, permission)
}

func cameraContext(request *http.Request) (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if request != nil {
		ctx = request.Context()
	}
	return context.WithTimeout(ctx, cameraOperationTimeout)
}

type cameraSnapshotRequest struct {
	AID          uint64 `json:"aid"`
	ResourceType string `json:"resource-type"`
	Width        int    `json:"image-width"`
	Height       int    `json:"image-height"`
	Reason       int    `json:"reason,omitempty"`
}

func newCameraSnapshotHandler(
	authorized func(*http.Request) bool,
	media CameraMedia,
	devicesByAID map[uint64]string,
) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			response.Header().Set("Allow", http.MethodPost)
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if authorized == nil || !authorized(request) {
			// HAP uses 470 for an encrypted endpoint without a verified
			// controller session.
			response.WriteHeader(470)
			return
		}
		if media == nil {
			http.Error(response, "camera media unavailable", http.StatusServiceUnavailable)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maxSnapshotRequestBody)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var item cameraSnapshotRequest
		if err := decoder.Decode(&item); err != nil {
			http.Error(response, "invalid snapshot request", http.StatusBadRequest)
			return
		}
		if err := ensureSnapshotRequestEOF(decoder); err != nil {
			http.Error(response, "invalid snapshot request", http.StatusBadRequest)
			return
		}
		deviceID, found := devicesByAID[item.AID]
		if !found || item.ResourceType != "image" ||
			item.Width < 1 || item.Width > maxSnapshotDimension ||
			item.Height < 1 || item.Height > maxSnapshotDimension {
			http.Error(response, "invalid snapshot request", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), cameraOperationTimeout)
		defer cancel()
		image, err := media.Snapshot(ctx, deviceID, item.Width, item.Height)
		if err != nil {
			http.Error(response, "camera snapshot unavailable", http.StatusServiceUnavailable)
			return
		}
		if len(image) > maxSnapshotBytes || !validJPEG(image) {
			http.Error(response, "camera returned invalid JPEG", http.StatusBadGateway)
			return
		}
		response.Header().Set("Content-Type", "image/jpeg")
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Length", fmt.Sprintf("%d", len(image)))
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(image)
	}
}

func ensureSnapshotRequestEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func validJPEG(value []byte) bool {
	return len(value) >= 4 &&
		value[0] == 0xff && value[1] == 0xd8 &&
		value[len(value)-2] == 0xff && value[len(value)-1] == 0xd9
}
