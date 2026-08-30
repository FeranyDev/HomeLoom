package matter

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	"go.uber.org/zap"
)

const protocolVersion = "1.1"

const defaultRuntimeBinary = "homeloom-matter-runtime"

type EndpointStorage interface {
	AllocateEndpoint(context.Context, string, string, device.Type) (uint16, error)
	TombstoneEndpoint(context.Context, string, string) error
	Endpoints(context.Context, string) ([]domaintarget.MatterEndpointIdentity, error)
}

type RuntimeStorage interface {
	Put(context.Context, string, string, []byte) error
	Get(context.Context, string, string) ([]byte, bool, error)
	List(context.Context, string) ([]domaintarget.MatterRuntimeValue, error)
	Delete(context.Context, string, string) error
	Clear(context.Context, string) error
}

type Storage interface {
	EndpointStorage
	RuntimeStorage
}

// CameraMediaRuntime is the narrow, target-scoped media capability exposed to
// the Matter sidecar. The sidecar never receives camera source URLs or
// credentials; Target selects the stream from its persisted device binding.
type CameraMediaRuntime interface {
	WebRTC(context.Context, string, CameraWebRTCRequest) (CameraWebRTCResponse, error)
	Snapshot(context.Context, string, uint16, uint16) ([]byte, error)
}

type CameraWebRTCRequest struct {
	Operation string              `json:"operation"`
	SessionID string              `json:"sessionId,omitempty"`
	SDP       string              `json:"sdp,omitempty"`
	Candidate *CameraICECandidate `json:"candidate,omitempty"`
}

type CameraICECandidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

type CameraWebRTCResponse struct {
	SessionID string `json:"sessionId,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	Closed    bool   `json:"closed,omitempty"`
}

type Config struct {
	ID          string
	Name        string
	NodeKind    string
	Matter      domaintarget.MatterConfig
	Devices     []domaintarget.VirtualDevice
	Executable  string
	ScriptPath  string
	SocketPath  string
	QueueSize   int
	StartWait   time.Duration
	RestartWait time.Duration
	OnStatus    func()
	CameraMedia CameraMediaRuntime
	LogLevel    string
	LogWriter   io.Writer
}

type CommissioningState struct {
	State             string
	WindowOpen        bool
	WindowExpiresAt   string
	FabricCount       int
	Fabrics           []FabricSummary
	EndpointCount     int
	UDPPort           uint16
	ManualPairingCode string
	SetupPayload      string
}

type FabricSummary struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type Target struct {
	config  Config
	devices *application.DeviceService
	storage Storage
	logger  *zap.Logger

	mu            sync.RWMutex
	client        *Client
	clientChanged chan struct{}
	initialReady  chan struct{}
	readyOnce     sync.Once
	status        CommissioningState
	revision      atomic.Uint64
	syncRequested chan struct{}

	snapshotMu    sync.Mutex
	lastSnapshots []deviceSnapshot
}

func New(config Config, devices *application.DeviceService, storage Storage, logger *zap.Logger) (*Target, error) {
	if config.ID == "" || config.Matter.Discriminator == nil {
		return nil, errors.New("Matter target ID and discriminator are required")
	}
	if devices == nil || storage == nil {
		return nil, errors.New("Matter target requires device and persistent storage services")
	}
	if config.NodeKind == "" {
		config.NodeKind = "bridge"
	}
	if config.NodeKind != "bridge" && config.NodeKind != "camera" {
		return nil, fmt.Errorf("unsupported Matter node kind %q", config.NodeKind)
	}
	if config.NodeKind == "camera" &&
		(len(config.Devices) != 1 || config.Devices[0].Type != device.TypeCamera || !config.Devices[0].Enabled ||
			len(config.Devices[0].AuxiliarySourceDeviceIDs) != 0) {
		return nil, errors.New("Matter camera node requires exactly one enabled camera without auxiliary sources")
	}
	if config.NodeKind == "camera" && config.CameraMedia == nil {
		return nil, errors.New("Matter camera node requires a media runtime")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	logger = logger.With(zap.String("module", "matter-target"))
	if config.Executable == "" && config.ScriptPath == "" {
		var err error
		config.Executable, config.ScriptPath, err = resolveRuntimeLaunch()
		if err != nil {
			return nil, err
		}
	} else {
		if config.Executable == "" {
			config.Executable = "node"
		}
		if config.ScriptPath == "" && config.Executable == "node" {
			config.ScriptPath = runtimeScriptPath()
		}
	}
	if config.SocketPath == "" {
		config.SocketPath = filepath.Join(".cache", "matter-runtime", config.ID+".sock")
	}
	if config.QueueSize <= 0 {
		config.QueueSize = 128
	}
	if config.StartWait <= 0 {
		config.StartWait = 10 * time.Second
	}
	if config.RestartWait <= 0 {
		config.RestartWait = 500 * time.Millisecond
	}
	return &Target{
		config: config, devices: devices, storage: storage, logger: logger,
		status:        CommissioningState{State: "uncommissioned"},
		syncRequested: make(chan struct{}, 1),
		clientChanged: make(chan struct{}),
		initialReady:  make(chan struct{}),
	}, nil
}

// Ready closes after the first runtime handshake, state replay, and status
// refresh have completed. A socket connection alone is not sufficient because
// commissioning RPCs require an authenticated and replayed session.
func (t *Target) Ready() <-chan struct{} { return t.initialReady }

func (t *Target) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(t.config.SocketPath), 0o700); err != nil {
		return fmt.Errorf("create Matter socket directory: %w", err)
	}
	unsubscribe := t.devices.Subscribe(func(device.Device) { t.requestSync() })
	defer unsubscribe()
	go t.syncLoop(ctx)

	for {
		started, err := t.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if !started {
			return err
		}
		t.logger.Error("Matter runtime disconnected; restarting", zap.String("target_id", t.config.ID), zap.Error(err))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(t.config.RestartWait):
		}
	}
}

func (t *Target) runOnce(ctx context.Context) (bool, error) {
	if err := removeStaleSocket(t.config.SocketPath); err != nil {
		return false, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := t.runtimeCommand(childCtx)
	if err := command.Start(); err != nil {
		return false, fmt.Errorf("start Matter runtime: %w", err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- waitForMatterRuntime(command, t.config.LogWriter) }()
	client, err := t.connect(childCtx, processDone)
	if err != nil {
		cancel()
		<-processDone
		return false, err
	}
	defer func() {
		t.clearClient(client)
		_ = client.Close()
	}()
	if err := t.handshakeAndReplay(childCtx, client); err != nil {
		cancel()
		<-processDone
		return false, err
	}
	t.setClient(client)
	t.readyOnce.Do(func() { close(t.initialReady) })
	if t.config.OnStatus != nil {
		t.config.OnStatus()
	}
	select {
	case <-ctx.Done():
		cancel()
		<-processDone
		return true, nil
	case err := <-processDone:
		if err == nil {
			err = errors.New("Matter runtime exited cleanly and requested restart")
		}
		return true, fmt.Errorf("Matter runtime exited: %w", err)
	case <-client.Done():
		cancel()
		<-processDone
		return true, ErrClosed
	}
}

func waitForMatterRuntime(command *exec.Cmd, output io.Writer) error {
	err := command.Wait()
	flushChildLogWriter(output)
	return err
}

func flushChildLogWriter(output io.Writer) {
	if output == nil {
		return
	}
	if flusher, ok := output.(interface{ Flush() }); ok {
		flusher.Flush()
	}
}

func (t *Target) runtimeCommand(ctx context.Context) *exec.Cmd {
	arguments := make([]string, 0, 9)
	if t.config.ScriptPath != "" {
		arguments = append(arguments, t.config.ScriptPath)
	}
	arguments = append(arguments,
		"--socket", t.config.SocketPath, "--target", t.config.ID, "--identity-namespace", t.config.ID,
	)
	command := exec.CommandContext(ctx, t.config.Executable, arguments...)
	if t.config.LogLevel != "" {
		command.Env = append(os.Environ(), "HOMELOOM_MATTER_LOG_LEVEL="+t.config.LogLevel, "HOMELOOM_MATTER_LOG_FORMAT=json")
	}
	if t.config.LogWriter != nil {
		command.Stdout, command.Stderr = t.config.LogWriter, t.config.LogWriter
	} else {
		command.Stdout, command.Stderr = io.Discard, io.Discard
	}
	return command
}

func (t *Target) connect(ctx context.Context, processDone <-chan error) (*Client, error) {
	deadline := time.NewTimer(t.config.StartWait)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := Dial(ctx, t.config.SocketPath, ClientOptions{
			QueueCapacity: t.config.QueueSize, DefaultTimeout: 5 * time.Second,
			RequestHandler: t.handleRequest, NotificationHandler: t.handleNotification,
		})
		if err == nil {
			return client, nil
		}
		select {
		case processErr := <-processDone:
			return nil, fmt.Errorf("Matter runtime exited before IPC became ready: %w", processErr)
		case <-deadline.C:
			return nil, errors.New("Matter runtime IPC startup timed out")
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (t *Target) handshakeAndReplay(ctx context.Context, client *Client) error {
	var handshake struct {
		ProtocolVersion   string   `json:"protocolVersion"`
		TargetID          string   `json:"targetId"`
		IdentityNamespace string   `json:"identityNamespace"`
		ReplayRequired    bool     `json:"replayRequired"`
		Capabilities      []string `json:"capabilities"`
	}
	if err := client.Call(ctx, "runtime.handshake", map[string]any{
		"protocolVersion": protocolVersion, "targetId": t.config.ID, "clientName": "homeloom-go",
	}, &handshake); err != nil {
		return err
	}
	if handshake.ProtocolVersion != protocolVersion || handshake.TargetID != t.config.ID || handshake.IdentityNamespace != t.config.ID {
		return fmt.Errorf("Matter runtime handshake identity mismatch: %#v", handshake)
	}
	replay, err := t.replayState(ctx)
	if err != nil {
		return err
	}
	if err := client.Call(ctx, "state.replay", replay, nil); err != nil {
		return fmt.Errorf("replay Matter state: %w", err)
	}
	t.replaceSnapshotBaseline(replay.Devices)
	t.mu.Lock()
	t.status.EndpointCount = len(replay.Devices)
	t.mu.Unlock()
	return t.refreshStatus(ctx, client)
}

type bridgeConfiguration struct {
	TargetID                   string `json:"targetId"`
	IdentityNamespace          string `json:"identityNamespace"`
	NodeKind                   string `json:"nodeKind"`
	NetworkInterface           string `json:"networkInterface,omitempty"`
	ListenPort                 uint16 `json:"listenPort"`
	Discriminator              uint16 `json:"discriminator"`
	CommissioningPasscode      uint32 `json:"commissioningPasscode"`
	VendorID                   uint16 `json:"vendorId"`
	ProductID                  uint16 `json:"productId"`
	ProductName                string `json:"productName"`
	SerialNumber               string `json:"serialNumber"`
	CommissioningWindowSeconds uint32 `json:"commissioningWindowSeconds"`
}

type deviceSnapshot struct {
	ID          string                       `json:"id"`
	EndpointID  uint16                       `json:"endpointId"`
	DeviceType  string                       `json:"deviceType"`
	Name        string                       `json:"name"`
	Reachable   bool                         `json:"reachable"`
	Attributes  map[string]any               `json:"attributes"`
	Constraints map[string]numericConstraint `json:"constraints,omitempty"`
}

// numericConstraint is the device-defined numeric envelope carried alongside
// a Matter attribute. It lets the runtime advertise the actual usable range
// instead of only the broad Matter default.
type numericConstraint struct {
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	Step *float64 `json:"step,omitempty"`
}

type cameraMedia struct {
	DeviceID string `json:"deviceId"`
	StreamID string `json:"streamId"`
}

type replayState struct {
	Revision uint64              `json:"revision"`
	Bridge   bridgeConfiguration `json:"bridge"`
	Devices  []deviceSnapshot    `json:"devices"`
	Media    *cameraMedia        `json:"media,omitempty"`
}

func (t *Target) replayState(ctx context.Context) (replayState, error) {
	port, err := t.runtimeUDPPort()
	if err != nil {
		return replayState{}, err
	}
	passcode, err := strconv.ParseUint(t.config.Matter.Passcode, 10, 32)
	if err != nil {
		return replayState{}, fmt.Errorf("parse Matter commissioning passcode: %w", err)
	}
	snapshots, err := t.buildDeviceSnapshots(ctx)
	if err != nil {
		return replayState{}, err
	}
	state := replayState{
		Revision: t.revision.Add(1),
		Bridge: bridgeConfiguration{
			TargetID: t.config.ID, IdentityNamespace: t.config.ID,
			NodeKind:         t.config.NodeKind,
			NetworkInterface: t.config.Matter.NetworkInterface, ListenPort: port,
			Discriminator: *t.config.Matter.Discriminator, CommissioningPasscode: uint32(passcode),
			VendorID: t.config.Matter.VendorID, ProductID: t.config.Matter.ProductID,
			ProductName: t.config.Matter.ProductName, SerialNumber: t.config.Matter.SerialNumber,
			CommissioningWindowSeconds: t.config.Matter.CommissioningWindowSeconds,
		},
		Devices: snapshots,
	}
	if t.config.NodeKind == "camera" {
		sourceID := t.config.Devices[0].SourceDeviceID
		state.Media = &cameraMedia{DeviceID: sourceID, StreamID: cameraStreamID(sourceID)}
	}
	return state, nil
}

func (t *Target) runtimeUDPPort() (uint16, error) {
	if t.config.Matter.UDPPort != 0 {
		return t.config.Matter.UDPPort, nil
	}
	t.mu.RLock()
	port := t.status.UDPPort
	t.mu.RUnlock()
	if port != 0 {
		return port, nil
	}
	allocated, err := availableUDPPort()
	if err != nil {
		return 0, err
	}
	t.mu.Lock()
	if t.status.UDPPort == 0 {
		t.status.UDPPort = allocated
	}
	port = t.status.UDPPort
	t.mu.Unlock()
	return port, nil
}

func (t *Target) buildDeviceSnapshots(ctx context.Context) ([]deviceSnapshot, error) {
	if t.config.NodeKind == "camera" {
		return t.buildCameraSnapshot(ctx)
	}
	active := make(map[string]struct{})
	result := make([]deviceSnapshot, 0, len(t.config.Devices))
	for _, virtual := range t.config.Devices {
		if !virtual.Enabled {
			continue
		}
		active[virtual.ID] = struct{}{}
		endpointID, err := t.storage.AllocateEndpoint(ctx, t.config.ID, virtual.ID, virtual.Type)
		if err != nil {
			return nil, fmt.Errorf("allocate Matter endpoint for %q: %w", virtual.ID, err)
		}
		projected, err := t.devices.ProjectSourcesForConsumerInstance("matter", t.config.ID, virtual.ID, virtual.Type, virtual.SourceDeviceIDs())
		if err != nil {
			return nil, fmt.Errorf("project Matter device %q: %w", virtual.ID, err)
		}
		contract, supported := mapping.ConsumerContract("matter", virtual.Type)
		if !supported {
			return nil, fmt.Errorf("Matter device type %q is unsupported", virtual.Type)
		}
		attributes := make(map[string]any, len(contract.Parameters))
		constraints := make(map[string]numericConstraint, len(contract.Parameters))
		for _, parameter := range contract.Parameters {
			path := parameter.Source
			property, found := projected.Property(path.EndpointID, path.CapabilityID, path.PropertyID)
			if !found {
				if parameter.Level == device.ParameterRequired {
					return nil, fmt.Errorf("Matter device %q missing required property %s", virtual.ID, path)
				}
				continue
			}
			attributes[parameter.Target] = propertyValueJSON(property.Value)
			if constraint, numeric := matterNumericConstraint(property.Definition); numeric {
				constraints[parameter.Target] = constraint
			}
		}
		result = append(result, deviceSnapshot{
			ID: virtual.ID, EndpointID: endpointID, DeviceType: string(virtual.Type),
			Name: virtual.Name, Reachable: projected.IsOnline(), Attributes: attributes, Constraints: constraints,
		})
	}
	identities, err := t.storage.Endpoints(ctx, t.config.ID)
	if err != nil {
		return nil, err
	}
	for _, identity := range identities {
		if identity.Tombstone {
			continue
		}
		if _, exists := active[identity.ConsumerDeviceID]; !exists {
			if err := t.storage.TombstoneEndpoint(ctx, t.config.ID, identity.ConsumerDeviceID); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func (t *Target) buildCameraSnapshot(ctx context.Context) ([]deviceSnapshot, error) {
	virtual := t.config.Devices[0]
	sources, err := t.devices.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Matter camera source: %w", err)
	}
	for _, source := range sources {
		if source.ID != virtual.SourceDeviceID {
			continue
		}
		if source.Type != device.TypeCamera {
			return nil, fmt.Errorf("Matter camera source %q is not a camera", source.ID)
		}
		return []deviceSnapshot{{
			ID: virtual.ID, EndpointID: 1, DeviceType: string(device.TypeCamera),
			Name: virtual.Name, Reachable: source.IsOnline(), Attributes: map[string]any{},
		}}, nil
	}
	return nil, fmt.Errorf("Matter camera source %q was not found", virtual.SourceDeviceID)
}

func cameraStreamID(deviceID string) string {
	sum := sha256.Sum256([]byte(deviceID))
	return "camera-" + hex.EncodeToString(sum[:8])
}

func (t *Target) syncLoop(ctx context.Context) {
	for {
		select {
		case <-t.syncRequested:
			client := t.currentClient()
			if client == nil {
				continue
			}
			snapshots, err := t.buildDeviceSnapshots(ctx)
			if err == nil {
				err = t.synchronizeSnapshots(ctx, client, snapshots)
			}
			if err != nil && ctx.Err() == nil {
				t.logger.Warn("synchronize Matter device state failed", zap.String("target_id", t.config.ID), zap.Error(err))
			}
		case <-ctx.Done():
			return
		}
	}
}

type attributeUpdate struct {
	DeviceID string `json:"deviceId"`
	Path     string `json:"path"`
	Value    any    `json:"value"`
}

type reachabilityUpdate struct {
	DeviceID  string `json:"deviceId"`
	Reachable bool   `json:"reachable"`
}

func (t *Target) synchronizeSnapshots(ctx context.Context, client *Client, snapshots []deviceSnapshot) error {
	t.snapshotMu.Lock()
	defer t.snapshotMu.Unlock()
	replace, attributes, availability := diffDeviceSnapshots(t.lastSnapshots, snapshots)
	if replace {
		if err := client.Call(ctx, "devices.replace", map[string]any{"devices": snapshots}, nil); err != nil {
			return err
		}
	} else {
		if len(attributes) > 0 {
			if err := client.Call(ctx, "attribute.update", map[string]any{"updates": attributes}, nil); err != nil {
				return err
			}
		}
		if len(availability) > 0 {
			if err := client.Call(ctx, "availability.update", map[string]any{"updates": availability}, nil); err != nil {
				return err
			}
		}
	}
	t.lastSnapshots = cloneDeviceSnapshots(snapshots)
	return nil
}

func (t *Target) replaceSnapshotBaseline(snapshots []deviceSnapshot) {
	t.snapshotMu.Lock()
	t.lastSnapshots = cloneDeviceSnapshots(snapshots)
	t.snapshotMu.Unlock()
}

func diffDeviceSnapshots(previous, current []deviceSnapshot) (bool, []attributeUpdate, []reachabilityUpdate) {
	if len(previous) != len(current) {
		return true, nil, nil
	}
	previousByID := make(map[string]deviceSnapshot, len(previous))
	for _, snapshot := range previous {
		previousByID[snapshot.ID] = snapshot
	}
	attributes := make([]attributeUpdate, 0)
	availability := make([]reachabilityUpdate, 0)
	for _, next := range current {
		before, found := previousByID[next.ID]
		if !found || before.EndpointID != next.EndpointID || before.DeviceType != next.DeviceType || before.Name != next.Name || !reflect.DeepEqual(before.Constraints, next.Constraints) {
			return true, nil, nil
		}
		if before.Reachable != next.Reachable {
			availability = append(availability, reachabilityUpdate{DeviceID: next.ID, Reachable: next.Reachable})
		}
		paths := make([]string, 0, len(next.Attributes))
		for path := range next.Attributes {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			value := next.Attributes[path]
			if !reflect.DeepEqual(before.Attributes[path], value) {
				attributes = append(attributes, attributeUpdate{DeviceID: next.ID, Path: path, Value: value})
			}
		}
	}
	return false, attributes, availability
}

func cloneDeviceSnapshots(source []deviceSnapshot) []deviceSnapshot {
	result := make([]deviceSnapshot, len(source))
	for index, snapshot := range source {
		result[index] = snapshot
		result[index].Attributes = make(map[string]any, len(snapshot.Attributes))
		for path, value := range snapshot.Attributes {
			result[index].Attributes[path] = value
		}
		if snapshot.Constraints != nil {
			result[index].Constraints = make(map[string]numericConstraint, len(snapshot.Constraints))
			for path, constraint := range snapshot.Constraints {
				result[index].Constraints[path] = cloneNumericConstraint(constraint)
			}
		}
	}
	return result
}

func (t *Target) requestSync() {
	select {
	case t.syncRequested <- struct{}{}:
	default:
	}
}

func (t *Target) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "attribute.write":
		var request struct {
			TargetID string `json:"targetId"`
			DeviceID string `json:"deviceId"`
			Path     string `json:"path"`
			Value    any    `json:"value"`
		}
		if err := json.Unmarshal(params, &request); err != nil || request.TargetID != t.config.ID {
			return nil, &RPCError{Code: -32602, Message: "invalid attribute write"}
		}
		err := t.writeAttribute(ctx, request.DeviceID, request.Path, request.Value)
		if err != nil {
			return map[string]any{"accepted": false, "message": err.Error()}, nil
		}
		return map[string]any{"accepted": true}, nil
	case "cluster.command":
		var request struct {
			TargetID string         `json:"targetId"`
			DeviceID string         `json:"deviceId"`
			Path     string         `json:"path"`
			Fields   map[string]any `json:"fields"`
		}
		if err := json.Unmarshal(params, &request); err != nil || request.TargetID != t.config.ID {
			return nil, &RPCError{Code: -32602, Message: "invalid cluster command"}
		}
		err := t.executeClusterCommand(ctx, request.DeviceID, request.Path, request.Fields)
		if err != nil {
			return map[string]any{"accepted": false, "message": err.Error()}, nil
		}
		return map[string]any{"accepted": true}, nil
	case "storage.get":
		var request struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, err
		}
		value, found, err := t.storage.Get(ctx, t.config.ID, request.Key)
		return map[string]any{"found": found, "valueBase64": base64.StdEncoding.EncodeToString(value)}, err
	case "storage.put":
		var request struct {
			Key         string `json:"key"`
			ValueBase64 string `json:"valueBase64"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, err
		}
		value, err := base64.StdEncoding.DecodeString(request.ValueBase64)
		if err == nil {
			err = t.storage.Put(ctx, t.config.ID, request.Key, value)
		}
		return map[string]bool{"stored": err == nil}, err
	case "storage.delete":
		var request struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": true}, t.storage.Delete(ctx, t.config.ID, request.Key)
	case "storage.list":
		values, err := t.storage.List(ctx, t.config.ID)
		entries := make([]map[string]string, 0, len(values))
		for _, value := range values {
			entries = append(entries, map[string]string{"key": value.Key, "valueBase64": base64.StdEncoding.EncodeToString(value.Value)})
		}
		return map[string]any{"entries": entries}, err
	case "storage.clear":
		return map[string]bool{"cleared": true}, t.storage.Clear(ctx, t.config.ID)
	case "camera.webrtc":
		if t.config.NodeKind != "camera" || t.config.CameraMedia == nil {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}
		var request CameraWebRTCRequest
		if err := json.Unmarshal(params, &request); err != nil || !validCameraWebRTCRequest(request) {
			return nil, &RPCError{Code: -32602, Message: "invalid camera WebRTC request"}
		}
		return t.config.CameraMedia.WebRTC(ctx, cameraStreamID(t.config.Devices[0].SourceDeviceID), request)
	case "camera.snapshot":
		if t.config.NodeKind != "camera" || t.config.CameraMedia == nil {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}
		var request struct {
			Width  uint16 `json:"width"`
			Height uint16 `json:"height"`
		}
		if err := json.Unmarshal(params, &request); err != nil ||
			request.Width == 0 || request.Height == 0 || request.Width > 4096 || request.Height > 4096 {
			return nil, &RPCError{Code: -32602, Message: "invalid camera snapshot request"}
		}
		jpeg, err := t.config.CameraMedia.Snapshot(
			ctx, cameraStreamID(t.config.Devices[0].SourceDeviceID), request.Width, request.Height,
		)
		if err != nil {
			return nil, err
		}
		return map[string]string{"jpegBase64": base64.StdEncoding.EncodeToString(jpeg)}, nil
	default:
		return nil, &RPCError{Code: -32601, Message: "method not found"}
	}
}

func validCameraWebRTCRequest(request CameraWebRTCRequest) bool {
	if len(request.SessionID) > 128 || len(request.SDP) > 512<<10 {
		return false
	}
	switch request.Operation {
	case "open":
		return request.SessionID == "" && request.SDP != "" && request.Candidate == nil
	case "reoffer":
		return request.SessionID != "" && request.SDP != "" && request.Candidate == nil
	case "addIce":
		return request.SessionID != "" && request.SDP == "" && request.Candidate != nil &&
			request.Candidate.Candidate != "" && len(request.Candidate.Candidate) <= 4096
	case "close":
		return request.SessionID != "" && request.SDP == "" && request.Candidate == nil
	default:
		return false
	}
}

func (t *Target) handleNotification(method string, params json.RawMessage) {
	changed := false
	switch method {
	case "commissioning.changed":
		var event struct {
			State       string `json:"state"`
			WindowOpen  bool   `json:"windowOpen"`
			FabricCount int    `json:"fabricCount"`
		}
		if json.Unmarshal(params, &event) == nil {
			t.mu.Lock()
			t.status.State, t.status.WindowOpen, t.status.FabricCount = event.State, event.WindowOpen, event.FabricCount
			if !event.WindowOpen {
				t.status.WindowExpiresAt, t.status.ManualPairingCode, t.status.SetupPayload = "", "", ""
			}
			t.mu.Unlock()
			changed = true
		}
	case "fabric.changed":
		var event struct {
			Change      string `json:"change"`
			FabricID    string `json:"fabricId"`
			Label       string `json:"label"`
			FabricCount int    `json:"fabricCount"`
		}
		if json.Unmarshal(params, &event) == nil {
			t.mu.Lock()
			t.status.FabricCount = event.FabricCount
			switch event.Change {
			case "added":
				t.status.Fabrics = upsertFabric(t.status.Fabrics, FabricSummary{ID: event.FabricID, Label: event.Label})
			case "removed":
				t.status.Fabrics = removeFabric(t.status.Fabrics, event.FabricID)
			case "reset":
				t.status.Fabrics = nil
			}
			t.mu.Unlock()
			changed = true
		}
	case "runtime.diagnostics":
		var event struct {
			Level   string `json:"level"`
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &event) == nil && event.Level == "error" {
			t.logger.Error("Matter runtime diagnostic", zap.String("target_id", t.config.ID), zap.String("code", event.Code), zap.String("diagnostic_message", event.Message))
		}
	}
	if changed && t.config.OnStatus != nil {
		t.config.OnStatus()
	}
}

func (t *Target) writeAttribute(ctx context.Context, virtualID, matterPath string, raw any) error {
	virtual, found := t.virtualDevice(virtualID)
	if !found {
		return application.ErrDeviceNotFound
	}
	if virtual.Type == device.TypeFan && matterPath == "FanControl.FanMode" {
		return t.writeFanMode(ctx, virtual, raw)
	}
	contract, found := mapping.ConsumerContract("matter", virtual.Type)
	if !found {
		return application.ErrPropertyUnsupported
	}
	for _, parameter := range contract.Parameters {
		if parameter.Target != matterPath {
			continue
		}
		return t.writeModelProperty(ctx, virtual, parameter.ModelPath(), raw)
	}
	return application.ErrPropertyUnsupported
}

func (t *Target) writeFanMode(ctx context.Context, virtual domaintarget.VirtualDevice, raw any) error {
	writes, err := fanModeModelWrites(raw)
	if err != nil {
		return err
	}
	for _, write := range writes {
		if err := t.writeModelProperty(ctx, virtual, write.Path, write.Value); err != nil {
			return err
		}
	}
	return nil
}

type modelWrite struct {
	Path  device.ParameterPath
	Value any
}

func fanModeModelWrites(raw any) ([]modelWrite, error) {
	mode, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("Matter FanControl.FanMode value %v is not a string", raw)
	}
	activePath := device.ParameterPath{EndpointID: "main", CapabilityID: "fan", PropertyID: "active"}
	if mode == "off" {
		return []modelWrite{{Path: activePath, Value: false}}, nil
	}
	if mode != "manual" && mode != "auto" {
		return nil, fmt.Errorf("Matter FanControl.FanMode value %q is unsupported", mode)
	}
	return []modelWrite{
		{Path: activePath, Value: true},
		{Path: device.ParameterPath{EndpointID: "main", CapabilityID: "fan", PropertyID: "target-state"}, Value: mode},
	}, nil
}

func (t *Target) writeModelProperty(ctx context.Context, virtual domaintarget.VirtualDevice, path device.ParameterPath, raw any) error {
	model, found := device.ModelContractFor(virtual.Type)
	if !found {
		return application.ErrPropertyUnsupported
	}
	for _, definition := range model.Parameters {
		if definition.Path.Key() != path.Key() {
			continue
		}
		value, err := jsonPropertyValue(raw, definition.Type)
		if err != nil {
			return err
		}
		_, _, err = t.devices.ExecuteConsumerPropertySourcesInstance(ctx, "matter", t.config.ID, virtual.ID,
			virtual.Type, virtual.SourceDeviceIDs(), path.EndpointID, path.CapabilityID, path.PropertyID, value)
		return err
	}
	return application.ErrPropertyUnsupported
}

func (t *Target) executeClusterCommand(ctx context.Context, virtualID, path string, fields map[string]any) error {
	if path == "KeypadInput.SendKey" {
		virtual, found := t.virtualDevice(virtualID)
		if !found {
			return application.ErrDeviceNotFound
		}
		keyCode, ok := fields["keyCode"].(string)
		if !ok || keyCode == "" {
			return fmt.Errorf("Matter KeypadInput.SendKey keyCode %v is not a unified television key", fields["keyCode"])
		}
		return t.writeModelProperty(ctx, virtual, device.ParameterPath{EndpointID: "main", CapabilityID: "television", PropertyID: "remote-key"}, keyCode)
	}
	if modelPath, value, direct := directCommandModelWrite(path); direct {
		virtual, found := t.virtualDevice(virtualID)
		if !found {
			return application.ErrDeviceNotFound
		}
		return t.writeModelProperty(ctx, virtual, modelPath, value)
	}
	switch path {
	case "OnOff.On":
		if virtual, found := t.virtualDevice(virtualID); found && virtual.Type == device.TypeSpeaker {
			// Matter On means unmuted for Speaker device type.
			return t.writeAttribute(ctx, virtualID, "OnOff.OnOff", false)
		}
		return t.writeAttribute(ctx, virtualID, "OnOff.OnOff", true)
	case "OnOff.Off":
		if virtual, found := t.virtualDevice(virtualID); found && virtual.Type == device.TypeSpeaker {
			return t.writeAttribute(ctx, virtualID, "OnOff.OnOff", true)
		}
		return t.writeAttribute(ctx, virtualID, "OnOff.OnOff", false)
	case "LevelControl.MoveToLevel":
		return t.writeAttribute(ctx, virtualID, "LevelControl.CurrentLevel", fields["level"])
	case "WindowCovering.GoToLiftPercentage":
		return t.writeAttribute(ctx, virtualID, "WindowCovering.TargetPositionLiftPercent100ths", fields["liftPercent100ths"])
	case "ValveConfigurationAndControl.Open":
		if err := t.writeAttribute(ctx, virtualID, "ValveConfigurationAndControl.TargetState", true); err != nil {
			return err
		}
		if level, ok := fields["targetLevel"]; ok && level != nil {
			return t.writeAttribute(ctx, virtualID, "ValveConfigurationAndControl.CurrentLevel", level)
		}
		return nil
	case "ValveConfigurationAndControl.Close":
		return t.writeAttribute(ctx, virtualID, "ValveConfigurationAndControl.TargetState", false)
	default:
		return application.ErrPropertyUnsupported
	}
}

func directCommandModelWrite(path string) (device.ParameterPath, any, bool) {
	switch path {
	case "WindowCovering.StopMotion":
		return device.ParameterPath{
			EndpointID: "main", CapabilityID: "window-covering", PropertyID: "hold-position",
		}, true, true
	case "DoorLock.LockDoor":
		return device.ParameterPath{
			EndpointID: "main", CapabilityID: "lock", PropertyID: "target-state",
		}, "secured", true
	case "DoorLock.UnlockDoor":
		return device.ParameterPath{
			EndpointID: "main", CapabilityID: "lock", PropertyID: "target-state",
		}, "unsecured", true
	case "MediaPlayback.Play":
		return device.ParameterPath{
			EndpointID: "main", CapabilityID: "television", PropertyID: "target-media-state",
		}, "play", true
	case "MediaPlayback.Pause":
		return device.ParameterPath{
			EndpointID: "main", CapabilityID: "television", PropertyID: "target-media-state",
		}, "pause", true
	case "MediaPlayback.Stop":
		return device.ParameterPath{
			EndpointID: "main", CapabilityID: "television", PropertyID: "target-media-state",
		}, "stop", true
	default:
		return device.ParameterPath{}, nil, false
	}
}

func (t *Target) virtualDevice(id string) (domaintarget.VirtualDevice, bool) {
	for _, current := range t.config.Devices {
		if current.ID == id && current.Enabled {
			return current, true
		}
	}
	return domaintarget.VirtualDevice{}, false
}

func (t *Target) OpenCommissioningWindow(ctx context.Context, durationSeconds uint32) error {
	ctx, cancel := t.runtimeOperationContext(ctx)
	defer cancel()
	if err := t.callRuntime(ctx, "commissioning.open", map[string]uint32{"durationSeconds": durationSeconds}, nil, true); err != nil {
		return err
	}
	return t.refreshCurrentStatus(ctx)
}

func (t *Target) CloseCommissioningWindow(ctx context.Context) error {
	ctx, cancel := t.runtimeOperationContext(ctx)
	defer cancel()
	if err := t.callRuntime(ctx, "commissioning.close", map[string]any{}, nil, true); err != nil {
		return err
	}
	return t.refreshCurrentStatus(ctx)
}

func (t *Target) RemoveFabric(ctx context.Context, fabricID string) error {
	ctx, cancel := t.runtimeOperationContext(ctx)
	defer cancel()
	if err := t.callRuntime(ctx, "fabric.remove", map[string]string{"fabricId": fabricID}, nil, false); err != nil {
		return err
	}
	return t.refreshCurrentStatus(ctx)
}

func (t *Target) FactoryReset(ctx context.Context) error {
	ctx, cancel := t.runtimeOperationContext(ctx)
	defer cancel()
	if err := t.callRuntime(ctx, "identity.factoryReset", map[string]any{}, nil, false); err != nil {
		return err
	}
	return t.refreshCurrentStatus(ctx)
}

type runtimeStatus struct {
	FabricCount             int             `json:"fabricCount"`
	Fabrics                 []FabricSummary `json:"fabrics"`
	CommissioningWindowOpen bool            `json:"commissioningWindowOpen"`
	CommissioningExpiresAt  string          `json:"commissioningWindowExpiresAt"`
	ManualPairingCode       string          `json:"manualPairingCode"`
	QRPairingCode           string          `json:"qrPairingCode"`
}

func (t *Target) refreshStatus(ctx context.Context, client *Client) error {
	var status runtimeStatus
	if err := client.Call(ctx, "runtime.status", map[string]any{}, &status); err != nil {
		return err
	}
	t.applyRuntimeStatus(status)
	return nil
}

func (t *Target) refreshCurrentStatus(ctx context.Context) error {
	var status runtimeStatus
	if err := t.callRuntime(ctx, "runtime.status", map[string]any{}, &status, true); err != nil {
		return err
	}
	t.applyRuntimeStatus(status)
	return nil
}

func (t *Target) applyRuntimeStatus(status runtimeStatus) {
	t.mu.Lock()
	t.status.FabricCount = status.FabricCount
	t.status.Fabrics = append([]FabricSummary(nil), status.Fabrics...)
	t.status.WindowOpen = status.CommissioningWindowOpen
	t.status.WindowExpiresAt = status.CommissioningExpiresAt
	t.status.ManualPairingCode = status.ManualPairingCode
	t.status.SetupPayload = status.QRPairingCode
	if status.FabricCount > 0 {
		t.status.State = "commissioned"
	} else {
		t.status.State = "uncommissioned"
	}
	if status.CommissioningWindowOpen {
		t.status.State = "window-open"
	} else {
		t.status.ManualPairingCode, t.status.SetupPayload = "", ""
	}
	t.mu.Unlock()
}

func (t *Target) Status() CommissioningState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	status := t.status
	status.Fabrics = append([]FabricSummary(nil), t.status.Fabrics...)
	return status
}

func upsertFabric(fabrics []FabricSummary, next FabricSummary) []FabricSummary {
	if next.ID == "" {
		return fabrics
	}
	result := append([]FabricSummary(nil), fabrics...)
	for index := range result {
		if result[index].ID == next.ID {
			result[index] = next
			return result
		}
	}
	return append(result, next)
}

func removeFabric(fabrics []FabricSummary, id string) []FabricSummary {
	result := make([]FabricSummary, 0, len(fabrics))
	for _, fabric := range fabrics {
		if fabric.ID != id {
			result = append(result, fabric)
		}
	}
	return result
}

func (t *Target) setClient(client *Client) {
	t.mu.Lock()
	t.client = client
	close(t.clientChanged)
	t.clientChanged = make(chan struct{})
	t.mu.Unlock()
}

func (t *Target) clearClient(client *Client) {
	t.mu.Lock()
	if t.client == client {
		t.client = nil
		close(t.clientChanged)
		t.clientChanged = make(chan struct{})
	}
	t.mu.Unlock()
}

func (t *Target) currentClient() *Client {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.client
}

func (t *Target) runtimeOperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, t.config.StartWait)
}

func (t *Target) waitForClient(ctx context.Context) (*Client, error) {
	for {
		t.mu.RLock()
		client, changed := t.client, t.clientChanged
		t.mu.RUnlock()
		if client != nil {
			select {
			case <-client.Done():
				// clearClient rotates changed as runOnce unwinds.
			default:
				return client, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("Matter Runtime is not ready: %w", ctx.Err())
		case <-changed:
		}
	}
}

func (t *Target) callRuntime(ctx context.Context, method string, params, result any, retryOnDisconnect bool) error {
	for {
		client, err := t.waitForClient(ctx)
		if err != nil {
			return err
		}
		err = client.Call(ctx, method, params, result)
		if err == nil {
			return nil
		}
		if !retryOnDisconnect || ctx.Err() != nil {
			return err
		}
		select {
		case <-client.Done():
			continue
		default:
			return err
		}
	}
}

func propertyValueJSON(value device.PropertyValue) any {
	switch value.Type {
	case device.ValueTypeBool:
		if value.Bool != nil {
			return *value.Bool
		}
	case device.ValueTypeInt:
		if value.Int != nil {
			return *value.Int
		}
	case device.ValueTypeNumber:
		if value.Number != nil {
			return *value.Number
		}
	case device.ValueTypeString, device.ValueTypeEnum:
		if value.String != nil {
			return *value.String
		}
	}
	return nil
}

func matterNumericConstraint(definition device.PropertyDefinition) (numericConstraint, bool) {
	if definition.Type != device.ValueTypeInt && definition.Type != device.ValueTypeNumber {
		return numericConstraint{}, false
	}
	if definition.Min == nil && definition.Max == nil && definition.Step == nil {
		return numericConstraint{}, false
	}
	return numericConstraint{Min: cloneFloat(definition.Min), Max: cloneFloat(definition.Max), Step: cloneFloat(definition.Step)}, true
}

func cloneNumericConstraint(value numericConstraint) numericConstraint {
	return numericConstraint{Min: cloneFloat(value.Min), Max: cloneFloat(value.Max), Step: cloneFloat(value.Step)}
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func jsonPropertyValue(raw any, valueType device.ValueType) (device.PropertyValue, error) {
	switch valueType {
	case device.ValueTypeBool:
		value, ok := raw.(bool)
		if ok {
			return device.BoolValue(value), nil
		}
	case device.ValueTypeInt:
		value, ok := raw.(float64)
		if ok {
			return device.IntValue(int64(value)), nil
		}
	case device.ValueTypeNumber:
		value, ok := raw.(float64)
		if ok {
			return device.NumberValue(value), nil
		}
	case device.ValueTypeString:
		value, ok := raw.(string)
		if ok {
			return device.StringValue(value), nil
		}
	case device.ValueTypeEnum:
		value, ok := raw.(string)
		if ok {
			return device.EnumValue(value), nil
		}
	}
	return device.PropertyValue{}, fmt.Errorf("Matter value %v does not match %s", raw, valueType)
}

func availableUDPPort() (uint16, error) {
	conn, err := net.ListenPacket("udp6", "[::]:0")
	if err != nil {
		conn, err = net.ListenPacket("udp4", "0.0.0.0:0")
	}
	if err != nil {
		return 0, fmt.Errorf("allocate Matter UDP port: %w", err)
	}
	defer conn.Close()
	_, portText, _ := net.SplitHostPort(conn.LocalAddr().String())
	port, err := strconv.ParseUint(portText, 10, 16)
	return uint16(port), err
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Matter socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to remove non-socket Matter IPC path %q", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale Matter socket: %w", err)
	}
	return nil
}

func runtimeScriptPath() string {
	if configured := strings.TrimSpace(os.Getenv("HOMELOOM_MATTER_RUNTIME")); configured != "" {
		if isJavaScriptRuntimePath(configured) {
			return configured
		}
	}
	return firstExistingRuntimePath(runtimeScriptCandidates(), filepath.Join("matter-runtime", "dist", "src", "cli.js"))
}

func resolveRuntimeLaunch() (executable, scriptPath string, err error) {
	scriptPath = firstExistingRuntimePath(runtimeScriptCandidates(), "")
	nodeAvailable := nodeRuntimeAvailable()
	if scriptPath != "" && nodeAvailable {
		return "node", scriptPath, nil
	}
	if binary := firstExecutableRuntimePath(runtimeBinaryCandidates()); binary != "" {
		return binary, "", nil
	}
	if scriptPath == "" {
		return "", "", errors.New("Matter runtime unavailable: JavaScript entry was not found and executable homeloom-matter-runtime was not found; install Node.js with matter-runtime/dist/src/cli.js or place homeloom-matter-runtime next to HomeLoom")
	}
	if !nodeAvailable {
		return "", "", fmt.Errorf("Matter runtime unavailable: JavaScript entry %q exists but Node.js was not found, and executable homeloom-matter-runtime was not found; install Node.js or place homeloom-matter-runtime next to HomeLoom", scriptPath)
	}
	return "", "", fmt.Errorf("Matter runtime unavailable: JavaScript entry %q could not be started and executable homeloom-matter-runtime was not found", scriptPath)
}

func runtimeScriptCandidates() []string {
	relative := filepath.Join("matter-runtime", "dist", "src", "cli.js")
	candidates := make([]string, 0, 8)
	if configured := strings.TrimSpace(os.Getenv("HOMELOOM_MATTER_RUNTIME")); configured != "" && isJavaScriptRuntimePath(configured) {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates, relative, filepath.Join("..", relative))
	if executable, err := os.Executable(); err == nil {
		directory := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(directory, relative),
			filepath.Join(directory, "..", relative),
			filepath.Join(directory, "..", "..", relative),
		)
	}
	return candidates
}

func runtimeBinaryCandidates() []string {
	candidates := make([]string, 0, 6)
	if configured := strings.TrimSpace(os.Getenv("HOMELOOM_MATTER_RUNTIME")); configured != "" && !isJavaScriptRuntimePath(configured) {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates, defaultRuntimeBinary, filepath.Join("..", defaultRuntimeBinary))
	if executable, err := os.Executable(); err == nil {
		directory := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(directory, defaultRuntimeBinary),
			filepath.Join(directory, "..", defaultRuntimeBinary),
			filepath.Join(directory, "..", "..", defaultRuntimeBinary),
		)
	}
	return candidates
}

func firstExistingRuntimePath(candidates []string, fallback string) string {
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(absolute); err == nil && info.Mode().IsRegular() {
			return absolute
		}
	}
	return fallback
}

func firstExecutableRuntimePath(candidates []string) string {
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(absolute); err == nil && isExecutableRuntimeFile(absolute, info) {
			return absolute
		}
	}
	return ""
}

func isExecutableRuntimeFile(path string, info os.FileInfo) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	return info.Mode()&0o111 != 0 || strings.EqualFold(filepath.Ext(path), ".exe")
}

func nodeRuntimeAvailable() bool {
	_, err := exec.LookPath("node")
	return err == nil
}

func isJavaScriptRuntimePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}
