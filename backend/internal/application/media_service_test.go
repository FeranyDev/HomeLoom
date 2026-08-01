package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/media"
)

type mediaServiceStoreStub struct {
	mu          sync.Mutex
	generation  uint64
	revision    uint64
	streams     map[string]media.StreamSpec
	saveCalls   int
	deleteCalls int
	writeErr    error
}

func newMediaServiceStoreStub() *mediaServiceStoreStub {
	return &mediaServiceStoreStub{
		generation: 1,
		revision:   1,
		streams:    make(map[string]media.StreamSpec),
	}
}

func (s *mediaServiceStoreStub) ListMediaStreams(context.Context) ([]media.StreamSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]media.StreamSpec, 0, len(s.streams))
	for _, item := range s.streams {
		result = append(result, cloneStreamSpec(item))
	}
	return result, nil
}

func (s *mediaServiceStoreStub) SaveMediaStream(_ context.Context, item media.StreamSpec) (MediaConfigVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.writeErr != nil {
		return MediaConfigVersion{}, s.writeErr
	}
	s.revision++
	s.streams[item.ID] = cloneStreamSpec(item)
	return MediaConfigVersion{Generation: s.generation, Revision: s.revision}, nil
}

func (s *mediaServiceStoreStub) DeleteMediaStream(_ context.Context, id string) (media.StreamSpec, MediaConfigVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls++
	if s.writeErr != nil {
		return media.StreamSpec{}, MediaConfigVersion{}, s.writeErr
	}
	item, exists := s.streams[id]
	if !exists {
		return media.StreamSpec{}, MediaConfigVersion{}, ErrMediaStreamNotFound
	}
	delete(s.streams, id)
	s.revision++
	return cloneStreamSpec(item), MediaConfigVersion{Generation: s.generation, Revision: s.revision}, nil
}

func (s *mediaServiceStoreStub) MediaStreamReplay(context.Context) (media.StreamReplay, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := make([]media.StreamSpec, 0, len(s.streams))
	for _, item := range s.streams {
		streams = append(streams, cloneStreamSpec(item))
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].ID < streams[j].ID })
	return media.StreamReplay{
		SchemaVersion: media.SchemaVersion,
		Generation:    s.generation,
		Revision:      s.revision,
		Streams:       streams,
	}, nil
}

type mediaServiceRuntimeStub struct {
	mu        sync.Mutex
	mutations []media.StreamMutation
	err       error
	ready     bool
}

func (r *mediaServiceRuntimeStub) PublishMediaStreamMutation(_ context.Context, mutation media.StreamMutation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mutations = append(r.mutations, cloneStreamMutation(mutation))
	return r.err
}

func (r *mediaServiceRuntimeStub) snapshot() []media.StreamMutation {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]media.StreamMutation, len(r.mutations))
	for index := range r.mutations {
		result[index] = cloneStreamMutation(r.mutations[index])
	}
	return result
}

func (r *mediaServiceRuntimeStub) MediaRuntimeReady() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready
}

func validMediaStream(id string) media.StreamSpec {
	return media.StreamSpec{
		SchemaVersion: media.SchemaVersion,
		ID:            id,
		DeviceID:      "camera-1",
		Protocol:      media.ProtocolRTSP,
		CredentialRef: "credential-1",
		Profile:       "main",
		Mode:          media.StreamPreload,
		Audio:         true,
		Options:       json.RawMessage(`{"transport":"tcp"}`),
	}
}

func TestMediaServiceCRUDPublishesVersionedMutations(t *testing.T) {
	ctx := context.Background()
	store := newMediaServiceStoreStub()
	runtime := &mediaServiceRuntimeStub{ready: true}
	service := NewMediaService(store, runtime)

	created, err := service.Create(ctx, validMediaStream("camera_stream-1"))
	if err != nil || created.ID != "camera_stream-1" {
		t.Fatalf("Create() = %#v, %v", created, err)
	}
	if _, err := service.Create(ctx, validMediaStream("camera_stream-1")); !errors.Is(err, ErrMediaStreamExists) {
		t.Fatalf("duplicate Create() error = %v", err)
	}
	updatedInput := validMediaStream("ignored")
	updatedInput.Mode = media.StreamAlwaysOn
	updated, err := service.Update(ctx, "camera_stream-1", updatedInput)
	if err != nil || updated.ID != "camera_stream-1" || updated.Mode != media.StreamAlwaysOn {
		t.Fatalf("Update() = %#v, %v", updated, err)
	}
	if _, err := service.Update(ctx, "missing", validMediaStream("ignored")); !errors.Is(err, ErrMediaStreamNotFound) {
		t.Fatalf("missing Update() error = %v", err)
	}
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 || items[0].Mode != media.StreamAlwaysOn {
		t.Fatalf("List() = %#v, %v", items, err)
	}
	if err := service.Delete(ctx, "camera_stream-1"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if err := service.Delete(ctx, "camera_stream-1"); !errors.Is(err, ErrMediaStreamNotFound) {
		t.Fatalf("missing Delete() error = %v", err)
	}

	mutations := runtime.snapshot()
	if len(mutations) != 3 {
		t.Fatalf("published mutations = %#v", mutations)
	}
	for index, mutation := range mutations {
		if mutation.Generation != 1 || mutation.Revision != uint64(index+2) {
			t.Errorf("mutation %d version = %d/%d", index, mutation.Generation, mutation.Revision)
		}
	}
	if mutations[0].Action != media.MutationUpsert || mutations[1].Action != media.MutationUpsert || mutations[2].Action != media.MutationDelete {
		t.Fatalf("mutation actions = %#v", mutations)
	}
}

func TestMediaServiceRejectsSecretsBeforePersistence(t *testing.T) {
	store := newMediaServiceStoreStub()
	runtime := &mediaServiceRuntimeStub{ready: true}
	service := NewMediaService(store, runtime)
	item := validMediaStream("camera_stream-1")
	item.Options = json.RawMessage(`{"accessToken":"secret-canary"}`)

	if _, err := service.Create(context.Background(), item); err == nil {
		t.Fatal("Create() accepted plaintext authorization material")
	}
	if store.saveCalls != 0 {
		t.Fatalf("store save calls = %d, want 0", store.saveCalls)
	}
	if mutations := runtime.snapshot(); len(mutations) != 0 {
		t.Fatalf("runtime received rejected mutation: %#v", mutations)
	}
}

func TestMediaServiceDoesNotPublishFailedPersistence(t *testing.T) {
	store := newMediaServiceStoreStub()
	store.writeErr = errors.New("database unavailable")
	runtime := &mediaServiceRuntimeStub{ready: true}
	service := NewMediaService(store, runtime)

	if _, err := service.Create(context.Background(), validMediaStream("camera_stream-1")); err == nil {
		t.Fatal("Create() succeeded with failed persistence")
	}
	if mutations := runtime.snapshot(); len(mutations) != 0 {
		t.Fatalf("runtime received uncommitted mutation: %#v", mutations)
	}
}

func TestMediaServiceRuntimeOfflineKeepsDurableDesiredStateForReplay(t *testing.T) {
	store := newMediaServiceStoreStub()
	runtimeError := errors.New("media runtime offline")
	runtime := &mediaServiceRuntimeStub{err: runtimeError, ready: true}
	service := NewMediaService(store, runtime)

	item, err := service.Create(context.Background(), validMediaStream("camera_stream-1"))
	if err != nil {
		t.Fatalf("offline Create() = %#v, %v", item, err)
	}
	if !errors.Is(service.LastRuntimeError(), runtimeError) {
		t.Fatalf("LastRuntimeError() = %v", service.LastRuntimeError())
	}
	if service.RuntimeStatus() != "degraded" {
		t.Fatalf("RuntimeStatus() = %q, want degraded", service.RuntimeStatus())
	}
	replay, err := service.Replay(context.Background())
	if err != nil || replay.Generation != 1 || replay.Revision != 2 || len(replay.Streams) != 1 {
		t.Fatalf("Replay() = %#v, %v", replay, err)
	}
	if replay.Streams[0].ID != item.ID {
		t.Fatalf("replay stream = %#v, created = %#v", replay.Streams[0], item)
	}

	runtime.err = nil
	if _, err := service.Create(context.Background(), validMediaStream("camera_stream-2")); err != nil {
		t.Fatal(err)
	}
	if service.LastRuntimeError() != nil {
		t.Fatalf("successful delivery did not clear runtime error: %v", service.LastRuntimeError())
	}
	if service.RuntimeStatus() != "ready" {
		t.Fatalf("RuntimeStatus() = %q, want ready", service.RuntimeStatus())
	}
}

func TestMediaServiceRuntimeStatus(t *testing.T) {
	if status := NewMediaService(newMediaServiceStoreStub(), nil).RuntimeStatus(); status != "disabled" {
		t.Fatalf("nil runtime status = %q", status)
	}
	runtime := &mediaServiceRuntimeStub{}
	if status := NewMediaService(newMediaServiceStoreStub(), runtime).RuntimeStatus(); status != "unavailable" {
		t.Fatalf("disconnected runtime status = %q", status)
	}
}

func TestMediaServiceSerializesConcurrentRevisionPublication(t *testing.T) {
	const count = 24
	store := newMediaServiceStoreStub()
	runtime := &mediaServiceRuntimeStub{ready: true}
	service := NewMediaService(store, runtime)

	var wait sync.WaitGroup
	errorsSeen := make(chan error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			id := fmt.Sprintf("camera_stream-%02d", index)
			_, err := service.Create(context.Background(), validMediaStream(id))
			errorsSeen <- err
		}(index)
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Create() = %v", err)
		}
	}

	mutations := runtime.snapshot()
	if len(mutations) != count {
		t.Fatalf("published mutation count = %d, want %d", len(mutations), count)
	}
	for index, mutation := range mutations {
		if want := uint64(index + 2); mutation.Revision != want {
			t.Fatalf("published revision %d = %d, want %d", index, mutation.Revision, want)
		}
	}
	replay, err := service.Replay(context.Background())
	if err != nil || len(replay.Streams) != count || replay.Revision != count+1 {
		t.Fatalf("Replay() = %d streams at revision %d, %v", len(replay.Streams), replay.Revision, err)
	}
}

func TestMediaServiceListAndReplayReturnClones(t *testing.T) {
	store := newMediaServiceStoreStub()
	service := NewMediaService(store, nil)
	if _, err := service.Create(context.Background(), validMediaStream("camera_stream-1")); err != nil {
		t.Fatal(err)
	}
	items, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items[0].Options[0] = 'X'
	replay, err := service.Replay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(replay.Streams[0].Options) != `{"transport":"tcp"}` {
		t.Fatalf("caller mutated persisted stream options: %s", replay.Streams[0].Options)
	}
	replay.Streams[0].Options[0] = 'X'
	second, err := service.Replay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(second.Streams[0].Options) != `{"transport":"tcp"}` {
		t.Fatalf("caller mutated replay source: %s", second.Streams[0].Options)
	}
}
