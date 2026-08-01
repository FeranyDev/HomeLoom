// Package media defines the transport-safe media contracts shared by the Core,
// Providers, and Media Worker. It deliberately contains no durable credential
// values: logical source and stream configuration may only refer to credentials
// by ID.
package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

const (
	SchemaVersion            = 1
	MaxLogicalConfigBytes    = 64 << 10
	MaxAuthorizationBytes    = 256 << 10
	MaxSessionDescriptionLen = 256 << 10
)

var (
	ErrInvalidContract = errors.New("invalid media contract")
	idPattern          = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9._:-]{0,126}[a-zA-Z0-9])?$`)
)

type Protocol string

const (
	ProtocolRTSP       Protocol = "rtsp"
	ProtocolONVIF      Protocol = "onvif"
	ProtocolXiaomiMISS Protocol = "xiaomi-miss"
)

func (p Protocol) Valid() bool {
	switch p {
	case ProtocolRTSP, ProtocolONVIF, ProtocolXiaomiMISS:
		return true
	default:
		return false
	}
}

type VideoCodec string

const (
	VideoCodecH264  VideoCodec = "h264"
	VideoCodecH265  VideoCodec = "h265"
	VideoCodecMJPEG VideoCodec = "mjpeg"
	VideoCodecVP8   VideoCodec = "vp8"
	VideoCodecVP9   VideoCodec = "vp9"
	VideoCodecAV1   VideoCodec = "av1"
)

func (c VideoCodec) Valid() bool {
	switch c {
	case VideoCodecH264, VideoCodecH265, VideoCodecMJPEG, VideoCodecVP8, VideoCodecVP9, VideoCodecAV1:
		return true
	default:
		return false
	}
}

type AudioCodec string

const (
	AudioCodecNone AudioCodec = "none"
	AudioCodecAAC  AudioCodec = "aac"
	AudioCodecOpus AudioCodec = "opus"
	AudioCodecPCMA AudioCodec = "pcma"
	AudioCodecPCMU AudioCodec = "pcmu"
	AudioCodecPCM  AudioCodec = "pcm"
)

func (c AudioCodec) Valid() bool {
	switch c {
	case AudioCodecNone, AudioCodecAAC, AudioCodecOpus, AudioCodecPCMA, AudioCodecPCMU, AudioCodecPCM:
		return true
	default:
		return false
	}
}

type StreamMode string

const (
	StreamOnDemand StreamMode = "on_demand"
	StreamPreload  StreamMode = "preload"
	StreamAlwaysOn StreamMode = "always_on"
)

func (m StreamMode) Valid() bool {
	return m == StreamOnDemand || m == StreamPreload || m == StreamAlwaysOn
}

type MutationAction string

const (
	MutationUpsert MutationAction = "upsert"
	MutationDelete MutationAction = "delete"
)

func (a MutationAction) Valid() bool { return a == MutationUpsert || a == MutationDelete }

type OperationAction string

const (
	OperationStart    OperationAction = "start"
	OperationStop     OperationAction = "stop"
	OperationRestart  OperationAction = "restart"
	OperationProbe    OperationAction = "probe"
	OperationSnapshot OperationAction = "snapshot"
)

func (a OperationAction) Valid() bool {
	switch a {
	case OperationStart, OperationStop, OperationRestart, OperationProbe, OperationSnapshot:
		return true
	default:
		return false
	}
}

type AuthorizationPurpose string

const (
	PurposePlayback  AuthorizationPurpose = "playback"
	PurposeSnapshot  AuthorizationPurpose = "snapshot"
	PurposeTalkback  AuthorizationPurpose = "talkback"
	PurposeProbe     AuthorizationPurpose = "probe"
	PurposeRecording AuthorizationPurpose = "recording"
)

func (p AuthorizationPurpose) Valid() bool {
	switch p {
	case PurposePlayback, PurposeSnapshot, PurposeTalkback, PurposeProbe, PurposeRecording:
		return true
	default:
		return false
	}
}

type AuthType string

const (
	AuthTypeNone    AuthType = "none"
	AuthTypeBasic   AuthType = "basic"
	AuthTypeDigest  AuthType = "digest"
	AuthTypeBearer  AuthType = "bearer"
	AuthTypeSession AuthType = "session"
	AuthTypeTURN    AuthType = "turn"
	AuthTypeVendor  AuthType = "vendor"
)

func (a AuthType) Valid() bool {
	switch a {
	case AuthTypeNone, AuthTypeBasic, AuthTypeDigest, AuthTypeBearer, AuthTypeSession, AuthTypeTURN, AuthTypeVendor:
		return true
	default:
		return false
	}
}

type SessionResult string

const (
	SessionConnected        SessionResult = "connected"
	SessionNetworkFailed    SessionResult = "network_failed"
	SessionCameraOffline    SessionResult = "camera_offline"
	SessionAuthFailed       SessionResult = "auth_failed"
	SessionCloudFailed      SessionResult = "cloud_failed"
	SessionProtocolFailed   SessionResult = "protocol_failed"
	SessionUnsupportedCodec SessionResult = "unsupported_codec"
	SessionExpired          SessionResult = "session_expired"
	SessionCancelled        SessionResult = "cancelled"
)

func (r SessionResult) Valid() bool {
	switch r {
	case SessionConnected, SessionNetworkFailed, SessionCameraOffline, SessionAuthFailed,
		SessionCloudFailed, SessionProtocolFailed, SessionUnsupportedCodec, SessionExpired,
		SessionCancelled:
		return true
	default:
		return false
	}
}

type MediaProfile struct {
	SchemaVersion int        `json:"schemaVersion"`
	ID            string     `json:"id"`
	Name          string     `json:"name,omitempty"`
	Width         int        `json:"width"`
	Height        int        `json:"height"`
	FPS           int        `json:"fps"`
	VideoCodec    VideoCodec `json:"videoCodec"`
	AudioCodec    AudioCodec `json:"audioCodec"`
	Bitrate       int64      `json:"bitrate,omitempty"`
}

func (p MediaProfile) Validate() error {
	if err := validateVersion(p.SchemaVersion); err != nil {
		return err
	}
	if !validID(p.ID) {
		return invalid("invalid media profile id %q", p.ID)
	}
	if p.Width <= 0 || p.Height <= 0 || p.FPS <= 0 {
		return invalid("profile %q dimensions and fps must be positive", p.ID)
	}
	if p.Bitrate < 0 {
		return invalid("profile %q bitrate cannot be negative", p.ID)
	}
	if !p.VideoCodec.Valid() || !p.AudioCodec.Valid() {
		return invalid("profile %q has unsupported codec", p.ID)
	}
	return nil
}

type MediaSource struct {
	SchemaVersion    int             `json:"schemaVersion"`
	DeviceID         string          `json:"deviceId"`
	ProviderID       string          `json:"providerId"`
	ProviderDeviceID string          `json:"providerDeviceId"`
	Protocol         Protocol        `json:"protocol"`
	CredentialRef    string          `json:"credentialRef,omitempty"`
	ConnectionMode   StreamMode      `json:"connectionMode,omitempty"`
	Profiles         []MediaProfile  `json:"profiles"`
	SourceConfig     json.RawMessage `json:"sourceConfig,omitempty"`
	Revision         uint64          `json:"revision"`
	Enabled          bool            `json:"enabled"`
}

// MediaSourceDescriptor is the Provider-facing discovery name for a
// MediaSource. It is an alias so discovery cannot create a second source model.
type MediaSourceDescriptor = MediaSource

func (s MediaSource) Validate() error {
	if err := validateVersion(s.SchemaVersion); err != nil {
		return err
	}
	if !device.ValidStableID(s.DeviceID) || !device.ValidStableID(s.ProviderID) || !validID(s.ProviderDeviceID) {
		return invalid("media source has an invalid device or provider id")
	}
	if !s.Protocol.Valid() {
		return invalid("unsupported media source protocol %q", s.Protocol)
	}
	if s.CredentialRef != "" && !validID(s.CredentialRef) {
		return invalid("invalid credential reference %q", s.CredentialRef)
	}
	if s.ConnectionMode != "" && !s.ConnectionMode.Valid() {
		return invalid("unsupported media source connection mode %q", s.ConnectionMode)
	}
	if s.Revision == 0 {
		return invalid("media source revision must be positive")
	}
	if err := ValidateLogicalConfig(s.SourceConfig); err != nil {
		return fmt.Errorf("%w: source config: %v", ErrInvalidContract, err)
	}
	seen := make(map[string]struct{}, len(s.Profiles))
	for _, profile := range s.Profiles {
		if err := profile.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[profile.ID]; duplicate {
			return invalid("duplicate media profile id %q", profile.ID)
		}
		seen[profile.ID] = struct{}{}
	}
	return nil
}

type StreamSpec struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	DeviceID      string          `json:"deviceId"`
	Protocol      Protocol        `json:"protocol"`
	CredentialRef string          `json:"credentialRef,omitempty"`
	Profile       string          `json:"profile"`
	Mode          StreamMode      `json:"mode"`
	Audio         bool            `json:"audio"`
	Talkback      bool            `json:"talkback"`
	Options       json.RawMessage `json:"options,omitempty"`
}

func (s StreamSpec) Validate() error {
	if err := validateVersion(s.SchemaVersion); err != nil {
		return err
	}
	if !validID(s.ID) || !device.ValidStableID(s.DeviceID) || !validID(s.Profile) {
		return invalid("stream has an invalid stream, device, or profile id")
	}
	if !s.Protocol.Valid() {
		return invalid("unsupported stream protocol %q", s.Protocol)
	}
	if s.CredentialRef != "" && !validID(s.CredentialRef) {
		return invalid("invalid credential reference %q", s.CredentialRef)
	}
	if !s.Mode.Valid() {
		return invalid("unsupported stream mode %q", s.Mode)
	}
	if s.Talkback && !s.Audio {
		return invalid("talkback requires audio")
	}
	if err := ValidateLogicalConfig(s.Options); err != nil {
		return fmt.Errorf("%w: stream options: %v", ErrInvalidContract, err)
	}
	return nil
}

type StreamReplay struct {
	SchemaVersion int          `json:"schemaVersion"`
	Generation    uint64       `json:"generation"`
	Revision      uint64       `json:"revision"`
	Streams       []StreamSpec `json:"streams"`
}

func (r StreamReplay) Validate() error {
	if err := validateEnvelope(r.SchemaVersion, r.Generation, r.Revision); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(r.Streams))
	for _, stream := range r.Streams {
		if err := stream.Validate(); err != nil {
			return fmt.Errorf("%w: stream %q: %v", ErrInvalidContract, stream.ID, err)
		}
		if _, duplicate := seen[stream.ID]; duplicate {
			return invalid("duplicate stream id %q in replay", stream.ID)
		}
		seen[stream.ID] = struct{}{}
	}
	return nil
}

type StreamMutation struct {
	SchemaVersion int            `json:"schemaVersion"`
	Generation    uint64         `json:"generation"`
	Revision      uint64         `json:"revision"`
	Action        MutationAction `json:"action"`
	Spec          StreamSpec     `json:"spec"`
}

func (m StreamMutation) Validate() error {
	if err := validateEnvelope(m.SchemaVersion, m.Generation, m.Revision); err != nil {
		return err
	}
	if !m.Action.Valid() {
		return invalid("unsupported stream mutation action %q", m.Action)
	}
	if err := m.Spec.Validate(); err != nil {
		return fmt.Errorf("%w: stream %q: %v", ErrInvalidContract, m.Spec.ID, err)
	}
	return nil
}

type MutationDecision string

const (
	MutationApply      MutationDecision = "apply"
	MutationDuplicate  MutationDecision = "duplicate"
	MutationStale      MutationDecision = "stale"
	MutationNeedReplay MutationDecision = "need_replay"
)

// Decision compares a mutation with the state already applied by a Worker.
// Only the next revision in the current generation may be applied. A repeated
// revision is idempotent; a new generation or a revision gap requires replay.
func (m StreamMutation) Decision(currentGeneration, currentRevision uint64) MutationDecision {
	if m.Generation < currentGeneration {
		return MutationStale
	}
	if m.Generation > currentGeneration || currentGeneration == 0 {
		return MutationNeedReplay
	}
	if m.Revision <= currentRevision {
		return MutationDuplicate
	}
	if m.Revision == currentRevision+1 {
		return MutationApply
	}
	return MutationNeedReplay
}

type StreamOperation struct {
	SchemaVersion int             `json:"schemaVersion"`
	RequestID     string          `json:"requestId"`
	StreamID      string          `json:"streamId"`
	Action        OperationAction `json:"action"`
}

func (o StreamOperation) Validate() error {
	if err := validateVersion(o.SchemaVersion); err != nil {
		return err
	}
	if !validID(o.RequestID) || !validID(o.StreamID) {
		return invalid("stream operation has an invalid request or stream id")
	}
	if !o.Action.Valid() {
		return invalid("unsupported stream operation action %q", o.Action)
	}
	return nil
}

type EndpointSpec struct {
	Protocol Protocol `json:"protocol"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Path     string   `json:"path,omitempty"`
}

func (e EndpointSpec) Validate() error {
	if !e.Protocol.Valid() || strings.TrimSpace(e.Host) == "" {
		return invalid("endpoint protocol and host are required")
	}
	if e.Port < 0 || e.Port > 65535 {
		return invalid("endpoint port %d is outside the valid range", e.Port)
	}
	if (e.Protocol == ProtocolRTSP || e.Protocol == ProtocolONVIF) && e.Port == 0 {
		return invalid("endpoint port is required for protocol %q", e.Protocol)
	}
	if strings.ContainsAny(e.Host, "/@?#") {
		return invalid("endpoint host must not contain URI components")
	}
	return nil
}

type AuthorizationRequest struct {
	SchemaVersion    int                  `json:"schemaVersion"`
	RequestID        string               `json:"requestId"`
	WorkerID         string               `json:"workerId"`
	WorkerInstanceID string               `json:"workerInstanceId"`
	DeviceID         string               `json:"deviceId"`
	Protocol         Protocol             `json:"protocol"`
	Purpose          AuthorizationPurpose `json:"purpose"`
	Attempt          int                  `json:"attempt"`
	ClientMaterial   json.RawMessage      `json:"clientMaterial,omitempty"`
	SessionOffer     []byte               `json:"sessionOffer,omitempty"`
}

func (r AuthorizationRequest) Validate() error {
	if err := validateVersion(r.SchemaVersion); err != nil {
		return err
	}
	if !validID(r.RequestID) || !validID(r.WorkerID) || !validID(r.WorkerInstanceID) || !device.ValidStableID(r.DeviceID) {
		return invalid("authorization request has an invalid request, worker, instance, or device id")
	}
	if !r.Protocol.Valid() || !r.Purpose.Valid() {
		return invalid("authorization request has an unsupported protocol or purpose")
	}
	if r.Attempt < 1 {
		return invalid("authorization attempt must be positive")
	}
	if len(r.SessionOffer) > MaxSessionDescriptionLen {
		return invalid("session offer exceeds %d bytes", MaxSessionDescriptionLen)
	}
	if err := validatePublicMaterial(r.ClientMaterial); err != nil {
		return fmt.Errorf("%w: client material: %v", ErrInvalidContract, err)
	}
	return nil
}

type AuthorizationResponse struct {
	SchemaVersion  int             `json:"schemaVersion"`
	LeaseID        string          `json:"leaseId"`
	ExpiresAt      time.Time       `json:"expiresAt"`
	Endpoint       EndpointSpec    `json:"endpoint"`
	AuthType       AuthType        `json:"authType"`
	PublicMaterial json.RawMessage `json:"publicMaterial,omitempty"`
	SecretMaterial json.RawMessage `json:"secretMaterial,omitempty"`
	SessionAnswer  []byte          `json:"sessionAnswer,omitempty"`
	Reusable       bool            `json:"reusable"`
	MaxUses        int             `json:"maxUses"`
}

func (r AuthorizationResponse) Validate() error {
	return r.ValidateAt(time.Now().UTC())
}

func (r AuthorizationResponse) ValidateAt(now time.Time) error {
	if err := validateVersion(r.SchemaVersion); err != nil {
		return err
	}
	if !validID(r.LeaseID) {
		return invalid("invalid lease id %q", r.LeaseID)
	}
	if !r.ExpiresAt.After(now) {
		return invalid("authorization lease must expire in the future")
	}
	if err := r.Endpoint.Validate(); err != nil {
		return err
	}
	if !r.AuthType.Valid() {
		return invalid("unsupported authorization type %q", r.AuthType)
	}
	if r.MaxUses < 1 || (!r.Reusable && r.MaxUses != 1) {
		return invalid("non-reusable authorization must have exactly one use")
	}
	if err := validateJSONMaterial(r.PublicMaterial, MaxAuthorizationBytes); err != nil {
		return fmt.Errorf("%w: public material: %v", ErrInvalidContract, err)
	}
	if err := validateJSONMaterial(r.SecretMaterial, MaxAuthorizationBytes); err != nil {
		return fmt.Errorf("%w: secret material: %v", ErrInvalidContract, err)
	}
	if len(r.SessionAnswer) > MaxSessionDescriptionLen {
		return invalid("session answer exceeds %d bytes", MaxSessionDescriptionLen)
	}
	return nil
}

type SessionReport struct {
	SchemaVersion    int           `json:"schemaVersion"`
	LeaseID          string        `json:"leaseId"`
	WorkerID         string        `json:"workerId"`
	WorkerInstanceID string        `json:"workerInstanceId"`
	DeviceID         string        `json:"deviceId"`
	Result           SessionResult `json:"result"`
	ErrorCode        string        `json:"errorCode,omitempty"`
	Detail           string        `json:"detail,omitempty"`
	ConnectedAt      time.Time     `json:"connectedAt,omitempty"`
	EndedAt          time.Time     `json:"endedAt,omitempty"`
}

func (r SessionReport) Validate() error {
	if err := validateVersion(r.SchemaVersion); err != nil {
		return err
	}
	if !validID(r.LeaseID) || !validID(r.WorkerID) || !validID(r.WorkerInstanceID) || !device.ValidStableID(r.DeviceID) {
		return invalid("session report has an invalid lease, worker, instance, or device id")
	}
	if !r.Result.Valid() {
		return invalid("unsupported session result %q", r.Result)
	}
	if r.ErrorCode != "" && !validID(r.ErrorCode) {
		return invalid("invalid session error code %q", r.ErrorCode)
	}
	if len(r.Detail) > 1024 {
		return invalid("session detail exceeds 1024 bytes")
	}
	if !r.ConnectedAt.IsZero() && !r.EndedAt.IsZero() && r.EndedAt.Before(r.ConnectedAt) {
		return invalid("session end precedes connection")
	}
	return nil
}

// ValidateLogicalConfig verifies the common part of the protocol-tagged
// SourceConfig and Options contracts. Protocol implementations must then call
// DecodeStrictConfig with their concrete schema to reject unknown fields.
func ValidateLogicalConfig(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if len(raw) > MaxLogicalConfigBytes {
		return invalid("logical configuration exceeds %d bytes", MaxLogicalConfigBytes)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return invalid("invalid logical configuration JSON: %v", err)
	}
	if err := ensureDecoderEOF(decoder); err != nil {
		return err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return invalid("logical configuration must be a JSON object")
	}
	return inspectLogicalValue(object, "")
}

// DecodeStrictConfig combines the media-domain secret check with Go's strict
// unknown-field rejection for a protocol-specific tagged configuration.
func DecodeStrictConfig(raw json.RawMessage, destination any) error {
	if destination == nil {
		return invalid("strict configuration destination is required")
	}
	if err := ValidateLogicalConfig(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalid("decode strict logical configuration: %v", err)
	}
	return ensureDecoderEOF(decoder)
}

func validatePublicMaterial(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if len(raw) > MaxAuthorizationBytes {
		return invalid("public material exceeds %d bytes", MaxAuthorizationBytes)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return invalid("invalid public material JSON: %v", err)
	}
	if err := ensureDecoderEOF(decoder); err != nil {
		return err
	}
	return inspectLogicalValue(value, "")
}

func validateJSONMaterial(raw json.RawMessage, maximum int) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if len(raw) > maximum {
		return invalid("JSON material exceeds %d bytes", maximum)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&value); err != nil {
		return invalid("invalid JSON material: %v", err)
	}
	return ensureDecoderEOF(decoder)
}

func inspectLogicalValue(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if sensitiveLogicalKey(key) {
				return invalid("logical configuration contains sensitive field %q", childPath)
			}
			if err := inspectLogicalValue(child, childPath); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := inspectLogicalValue(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case string:
		if strings.Contains(typed, "://") {
			parsed, err := url.Parse(typed)
			if err == nil && parsed.User != nil {
				return invalid("logical configuration URI %q contains user information", path)
			}
			if err == nil {
				for key := range parsed.Query() {
					if sensitiveLogicalKey(key) {
						return invalid("logical configuration URI %q contains sensitive query field %q", path, key)
					}
				}
			}
		}
	}
	return nil
}

func sensitiveLogicalKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	if normalized == "credentialref" || normalized == "keyid" || normalized == "publickey" || normalized == "clientpublic" {
		return false
	}
	for _, suffix := range []string{
		"password", "passwd", "passcode", "pin", "token", "secret", "secretmaterial",
		"privatekey", "sharedkey", "sessionkey", "apikey", "devicekey", "authkey",
		"authorization", "credential", "cookie", "signature", "sign",
	} {
		if normalized == suffix || strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func ensureDecoderEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return invalid("JSON contains multiple values")
		}
		return invalid("invalid trailing JSON: %v", err)
	}
	return nil
}

func validateEnvelope(schemaVersion int, generation, revision uint64) error {
	if err := validateVersion(schemaVersion); err != nil {
		return err
	}
	if generation == 0 || revision == 0 {
		return invalid("generation and revision must be positive")
	}
	return nil
}

func validateVersion(version int) error {
	if version != SchemaVersion {
		return invalid("unsupported schema version %d", version)
	}
	return nil
}

func validID(value string) bool { return idPattern.MatchString(value) }

func invalid(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidContract, fmt.Sprintf(format, arguments...))
}
