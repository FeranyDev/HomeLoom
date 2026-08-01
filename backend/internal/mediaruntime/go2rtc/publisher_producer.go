package go2rtc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/adapter"
	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
)

// PublisherProducerConfig uses deterministic port bands so every stream owns
// an independent HomeKit publisher and survives restart with the same address.
type PublisherProducerConfig struct {
	Binary       string
	RuntimeDir   string
	HAPHost      string
	HAPPortBase  int
	RTSPPortBase int
	SRTPPortBase int
	HomeKitPIN   string
	OnPairing    func(PublisherOutput)
	OnError      func(error)
}

type PublisherProducer struct {
	config  PublisherProducerConfig
	mu      sync.Mutex
	offsets map[string]int
	owners  map[int]string
}

func NewPublisherProducer(config PublisherProducerConfig) (*PublisherProducer, error) {
	if config.RuntimeDir == "" || config.HAPPortBase < 1024 || config.RTSPPortBase < 1024 || config.SRTPPortBase < 1024 {
		return nil, ErrInvalidConfig
	}
	if config.HAPHost == "" {
		config.HAPHost = "0.0.0.0"
	}
	return &PublisherProducer{
		config: config, offsets: make(map[string]int), owners: make(map[int]string),
	}, nil
}

func (p *PublisherProducer) Start(ctx context.Context, stream contract.StreamSpec, source adapter.Source) (adapter.Session, error) {
	parsed, err := url.Parse(source.URI)
	if err != nil || parsed.Host == "" || !sourceSchemeMatches(stream.Protocol, parsed.Scheme) {
		return p.fail(errors.New("invalid camera source"))
	}
	var options struct {
		Publisher string `json:"publisher"`
	}
	if len(stream.Options) != 0 {
		if err := decodeExact(stream.Options, &options); err != nil {
			return p.fail(errors.New("invalid camera stream options"))
		}
	}
	publishHomeKit := options.Publisher == "apple-home"
	ports, release, err := p.reservePorts(stream.ID)
	if err != nil {
		return p.fail(err)
	}
	publisher, err := StartPublisher(ctx, PublisherConfig{
		Binary: p.config.Binary, RuntimeDir: p.config.RuntimeDir, StreamID: stream.ID,
		APIListen:  net.JoinHostPort(p.config.HAPHost, strconv.Itoa(ports.hap)),
		RTSPListen: net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.rtsp)),
		SRTPListen: net.JoinHostPort(p.config.HAPHost, strconv.Itoa(ports.srtp)),
		SourceURI:  source.URI, SourceProtocol: stream.Protocol,
		ConnectionMode: stream.Mode, Audio: stream.Audio,
		Secrets:        PublisherSecrets{HomeKitPIN: p.config.HomeKitPIN},
		HomeKit:        HomeKitPublisher{Name: stream.DeviceID},
		PublishHomeKit: publishHomeKit,
	})
	if err != nil {
		release()
		return p.fail(err)
	}
	if err := writePublisherEndpoint(p.config.RuntimeDir, stream.ID, ports.hap); err != nil {
		_ = publisher.Close(context.Background())
		release()
		return p.fail(err)
	}
	if publishHomeKit && p.config.OnPairing != nil {
		p.config.OnPairing(publisher.Output())
	}
	return &publisherSession{publisher: publisher, release: release}, nil
}

func (p *PublisherProducer) fail(reason error) (adapter.Session, error) {
	if p.config.OnError != nil {
		p.config.OnError(reason)
	}
	return nil, errors.New("start camera media process failed")
}

func sourceSchemeMatches(protocol, scheme string) bool {
	if protocol == "xiaomi-miss" {
		return scheme == "xiaomi"
	}
	return protocol == scheme && (protocol == "rtsp" || protocol == "onvif")
}

type publisherSession struct {
	publisher *PublisherKernel
	release   func()
	once      sync.Once
	err       error
}

func (s *publisherSession) Close(ctx context.Context) error {
	s.once.Do(func() {
		s.err = s.publisher.Close(ctx)
		s.release()
	})
	return s.err
}
func (s *publisherSession) Output() PublisherOutput { return s.publisher.Output() }

type xiaomiAuthorizationPublic struct {
	UserID       string `json:"userId"`
	Region       string `json:"region"`
	DID          string `json:"did"`
	Model        string `json:"model"`
	LocalIP      string `json:"localIP"`
	Subtype      string `json:"subtype"`
	Channel      int    `json:"channel"`
	DevicePublic string `json:"devicePublic"`
	Vendor       string `json:"vendor"`
	UID          string `json:"uid,omitempty"`
}

func decodeExact(raw json.RawMessage, destination any) error {
	if len(raw) == 0 {
		return errors.New("empty material")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("extra JSON material")
	}
	return nil
}

type ports struct{ hap, rtsp, srtp int }

func (p *PublisherProducer) reservePorts(streamID string) (ports, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.offsets[streamID]; exists {
		return ports{}, nil, errors.New("camera stream ports are already allocated")
	}
	preferred := publisherPortOffset(streamID)
	if persisted, ok := readPublisherOffset(p.config.RuntimeDir, streamID, p.config.HAPPortBase); ok {
		preferred = persisted
	}
	offset := -1
	for attempt := 0; attempt < 1000; attempt++ {
		candidate := (preferred + attempt) % 1000
		if _, used := p.owners[candidate]; !used {
			offset = candidate
			break
		}
	}
	if offset < 0 {
		return ports{}, nil, errors.New("camera publisher port range is exhausted")
	}
	p.offsets[streamID] = offset
	p.owners[offset] = streamID
	release := func() {
		p.mu.Lock()
		if current, ok := p.offsets[streamID]; ok && current == offset {
			delete(p.offsets, streamID)
			delete(p.owners, offset)
		}
		p.mu.Unlock()
	}
	return ports{
		hap:  p.config.HAPPortBase + offset,
		rtsp: p.config.RTSPPortBase + offset,
		srtp: p.config.SRTPPortBase + offset,
	}, release, nil
}

func publisherPortOffset(streamID string) int {
	digest := sha256.Sum256([]byte(streamID))
	return (int(digest[0])<<8 | int(digest[1])) % 1000
}

func writePublisherEndpoint(runtimeDir, streamID string, hapPort int) error {
	raw, err := json.Marshal(struct {
		SchemaVersion int `json:"schemaVersion"`
		HAPPort       int `json:"hapPort"`
	}{SchemaVersion: 1, HAPPort: hapPort})
	if err != nil {
		return err
	}
	path := filepath.Join(runtimeDir, streamID, "publisher-endpoint.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return errors.New("persist camera publisher endpoint")
	}
	return os.Chmod(path, 0o600)
}

func readPublisherOffset(runtimeDir, streamID string, hapPortBase int) (int, bool) {
	raw, err := os.ReadFile(filepath.Join(runtimeDir, streamID, "publisher-endpoint.json"))
	if err != nil {
		return 0, false
	}
	var endpoint struct {
		SchemaVersion int `json:"schemaVersion"`
		HAPPort       int `json:"hapPort"`
	}
	if json.Unmarshal(raw, &endpoint) != nil || endpoint.SchemaVersion != 1 {
		return 0, false
	}
	offset := endpoint.HAPPort - hapPortBase
	return offset, offset >= 0 && offset < 1000
}
