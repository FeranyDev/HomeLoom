package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/media"
)

var (
	ErrMediaStreamNotFound         = errors.New("media stream not found")
	ErrMediaStreamExists           = errors.New("media stream already exists")
	ErrMediaStreamStoreUnavailable = errors.New("media stream store is unavailable")
)

type MediaConfigVersion struct {
	Generation uint64
	Revision   uint64
}

// MediaStreamStore is the application persistence boundary. Save and Delete
// must persist the desired-state change and advance the global media
// configuration revision in the same transaction.
type MediaStreamStore interface {
	ListMediaStreams(context.Context) ([]media.StreamSpec, error)
	SaveMediaStream(context.Context, media.StreamSpec) (MediaConfigVersion, error)
	DeleteMediaStream(context.Context, string) (media.StreamSpec, MediaConfigVersion, error)
	MediaStreamReplay(context.Context) (media.StreamReplay, error)
}

// MediaStreamRuntime publishes best-effort incremental desired-state changes.
// Runtime delivery is never the transaction boundary: persisted replay remains
// authoritative if the in-process media runtime has to be rebuilt.
type MediaStreamRuntime interface {
	PublishMediaStreamMutation(context.Context, media.StreamMutation) error
}

type mediaStreamRuntimeHealth interface {
	MediaRuntimeReady() bool
}

// MediaService serializes mutation publication so the media runtime never
// observes revisions reordered by concurrent application requests.
type MediaService struct {
	store   MediaStreamStore
	runtime MediaStreamRuntime

	mu               sync.Mutex
	lastRuntimeError error
}

func NewMediaService(store MediaStreamStore, runtime MediaStreamRuntime) *MediaService {
	return &MediaService{store: store, runtime: runtime}
}

func (s *MediaService) List(ctx context.Context) ([]media.StreamSpec, error) {
	if s == nil || s.store == nil {
		return nil, ErrMediaStreamStoreUnavailable
	}
	s.mu.Lock()
	items, err := s.store.ListMediaStreams(ctx)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	result := make([]media.StreamSpec, len(items))
	for index, item := range items {
		if err := item.Validate(); err != nil {
			return nil, fmt.Errorf("validate stored media stream %q: %w", item.ID, err)
		}
		result[index] = cloneStreamSpec(item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (s *MediaService) Create(ctx context.Context, item media.StreamSpec) (media.StreamSpec, error) {
	if err := validateMediaStreamSpec(item); err != nil {
		return media.StreamSpec{}, err
	}
	return s.save(ctx, item, true)
}

func (s *MediaService) Update(ctx context.Context, id string, item media.StreamSpec) (media.StreamSpec, error) {
	item.ID = id
	if err := validateMediaStreamSpec(item); err != nil {
		return media.StreamSpec{}, err
	}
	return s.save(ctx, item, false)
}

func (s *MediaService) Delete(ctx context.Context, id string) error {
	if s == nil || s.store == nil {
		return ErrMediaStreamStoreUnavailable
	}
	if !device.ValidStableID(id) {
		return NewValidationError("invalid media stream", map[string]string{"id": "must be a stable identifier"})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if exists, err := s.streamExistsLocked(ctx, id); err != nil {
		return err
	} else if !exists {
		return ErrMediaStreamNotFound
	}
	deleted, version, err := s.store.DeleteMediaStream(ctx, id)
	if err != nil {
		return err
	}
	if err := deleted.Validate(); err != nil {
		return fmt.Errorf("validate deleted media stream %q: %w", id, err)
	}
	if deleted.ID != id {
		return errors.New("media stream store returned a different deleted stream")
	}
	mutation, err := mediaStreamMutation(version, media.MutationDelete, deleted)
	if err != nil {
		return err
	}
	s.publishLocked(ctx, mutation)
	return nil
}

func (s *MediaService) Replay(ctx context.Context) (media.StreamReplay, error) {
	if s == nil || s.store == nil {
		return media.StreamReplay{}, ErrMediaStreamStoreUnavailable
	}
	s.mu.Lock()
	replay, err := s.store.MediaStreamReplay(ctx)
	s.mu.Unlock()
	if err != nil {
		return media.StreamReplay{}, err
	}
	if err := replay.Validate(); err != nil {
		return media.StreamReplay{}, fmt.Errorf("validate media stream replay: %w", err)
	}
	return cloneStreamReplay(replay), nil
}

// LastRuntimeError exposes delivery health without changing the success result
// of a durable write. It is cleared after the next successful publication.
func (s *MediaService) LastRuntimeError() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRuntimeError
}

// RuntimeStatus reports control-plane delivery readiness without exposing
// runtime implementation details through the HTTP API.
func (s *MediaService) RuntimeStatus() string {
	if s == nil || s.runtime == nil {
		return "disabled"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastRuntimeError != nil {
		return "degraded"
	}
	if health, ok := s.runtime.(mediaStreamRuntimeHealth); ok && !health.MediaRuntimeReady() {
		return "unavailable"
	}
	return "ready"
}

func (s *MediaService) save(ctx context.Context, item media.StreamSpec, create bool) (media.StreamSpec, error) {
	if s == nil || s.store == nil {
		return media.StreamSpec{}, ErrMediaStreamStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exists, err := s.streamExistsLocked(ctx, item.ID)
	if err != nil {
		return media.StreamSpec{}, err
	}
	if create && exists {
		return media.StreamSpec{}, ErrMediaStreamExists
	}
	if !create && !exists {
		return media.StreamSpec{}, ErrMediaStreamNotFound
	}
	item = cloneStreamSpec(item)
	version, err := s.store.SaveMediaStream(ctx, item)
	if err != nil {
		return media.StreamSpec{}, err
	}
	mutation, err := mediaStreamMutation(version, media.MutationUpsert, item)
	if err != nil {
		return media.StreamSpec{}, err
	}
	s.publishLocked(ctx, mutation)
	return cloneStreamSpec(item), nil
}

func (s *MediaService) streamExistsLocked(ctx context.Context, id string) (bool, error) {
	items, err := s.store.ListMediaStreams(ctx)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.ID == id {
			return true, nil
		}
	}
	return false, nil
}

func (s *MediaService) publishLocked(ctx context.Context, mutation media.StreamMutation) {
	if s.runtime != nil {
		s.lastRuntimeError = s.runtime.PublishMediaStreamMutation(ctx, cloneStreamMutation(mutation))
	}
}

func validateMediaStreamSpec(item media.StreamSpec) error {
	if err := item.Validate(); err != nil {
		return NewValidationError("invalid media stream", map[string]string{"stream": err.Error()})
	}
	return nil
}

func mediaStreamMutation(version MediaConfigVersion, action media.MutationAction, item media.StreamSpec) (media.StreamMutation, error) {
	mutation := media.StreamMutation{
		SchemaVersion: media.SchemaVersion,
		Generation:    version.Generation,
		Revision:      version.Revision,
		Action:        action,
		Spec:          cloneStreamSpec(item),
	}
	if err := mutation.Validate(); err != nil {
		return media.StreamMutation{}, fmt.Errorf("validate persisted media stream mutation: %w", err)
	}
	return mutation, nil
}

func cloneStreamSpec(item media.StreamSpec) media.StreamSpec {
	item.Options = append(json.RawMessage(nil), item.Options...)
	return item
}

func cloneStreamMutation(item media.StreamMutation) media.StreamMutation {
	item.Spec = cloneStreamSpec(item.Spec)
	return item
}

func cloneStreamReplay(item media.StreamReplay) media.StreamReplay {
	streams := item.Streams
	item.Streams = make([]media.StreamSpec, len(streams))
	for index := range streams {
		item.Streams[index] = cloneStreamSpec(streams[index])
	}
	return item
}
