package worker

import (
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
)

func testStream(id string) contract.StreamSpec {
	return contract.StreamSpec{
		SchemaVersion: 1, ID: id, DeviceID: "device-" + id, Protocol: "rtsp",
		Profile: "main", Mode: "on_demand",
	}
}

func TestReplayAndMutations(t *testing.T) {
	adapter := NewMemoryAdapter()
	manager := NewManager(adapter)
	result, err := manager.Replay(contract.ReplayParams{SchemaVersion: 1, Generation: 3, Revision: 10, Streams: []contract.StreamSpec{testStream("a")}})
	if err != nil || !result.Applied {
		t.Fatalf("replay = %#v, %v", result, err)
	}
	result, err = manager.Upsert(contract.UpsertParams{SchemaVersion: 1, Generation: 3, Revision: 11, Stream: testStream("b")})
	if err != nil || !result.Applied {
		t.Fatalf("upsert = %#v, %v", result, err)
	}
	result, err = manager.Delete(contract.DeleteParams{SchemaVersion: 1, Generation: 3, Revision: 12, StreamID: "a"})
	if err != nil || !result.Applied {
		t.Fatalf("delete = %#v, %v", result, err)
	}
	generation, revision, streams := manager.Snapshot()
	if generation != 3 || revision != 12 || len(streams) != 1 || streams[0].ID != "b" {
		t.Fatalf("unexpected snapshot: generation=%d revision=%d streams=%#v", generation, revision, streams)
	}
}

func TestDuplicateAndOutOfOrderMutationsDoNotChangeState(t *testing.T) {
	manager := NewManager(NewMemoryAdapter())
	_, err := manager.Replay(contract.ReplayParams{SchemaVersion: 1, Generation: 1, Revision: 4, Streams: []contract.StreamSpec{testStream("a")}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Upsert(contract.UpsertParams{SchemaVersion: 1, Generation: 1, Revision: 4, Stream: testStream("b")})
	if err != nil || result.Applied {
		t.Fatalf("duplicate = %#v, %v", result, err)
	}
	_, err = manager.Upsert(contract.UpsertParams{SchemaVersion: 1, Generation: 1, Revision: 6, Stream: testStream("b")})
	if !errors.Is(err, ErrRevisionGap) {
		t.Fatalf("gap error = %v", err)
	}
	_, err = manager.Upsert(contract.UpsertParams{SchemaVersion: 1, Generation: 1, Revision: 3, Stream: testStream("b")})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("old revision error = %v", err)
	}
	_, revision, streams := manager.Snapshot()
	if revision != 4 || len(streams) != 1 || streams[0].ID != "a" {
		t.Fatalf("state changed: revision=%d streams=%#v", revision, streams)
	}
}

func TestOldAndNewGenerationRules(t *testing.T) {
	manager := NewManager(NewMemoryAdapter())
	_, err := manager.Replay(contract.ReplayParams{SchemaVersion: 1, Generation: 2, Revision: 8, Streams: []contract.StreamSpec{testStream("a")}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Delete(contract.DeleteParams{SchemaVersion: 1, Generation: 1, Revision: 9, StreamID: "a"})
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("old generation error = %v", err)
	}
	_, err = manager.Upsert(contract.UpsertParams{SchemaVersion: 1, Generation: 3, Revision: 1, Stream: testStream("b")})
	if !errors.Is(err, ErrReplayRequired) {
		t.Fatalf("new generation mutation error = %v", err)
	}
	_, err = manager.Replay(contract.ReplayParams{SchemaVersion: 1, Generation: 3, Revision: 1, Streams: []contract.StreamSpec{testStream("b")}})
	if err != nil {
		t.Fatalf("new generation replay: %v", err)
	}
	generation, revision, streams := manager.Snapshot()
	if generation != 3 || revision != 1 || len(streams) != 1 || streams[0].ID != "b" {
		t.Fatalf("unexpected state: %d/%d %#v", generation, revision, streams)
	}
}

func TestInvalidReplayIsAtomic(t *testing.T) {
	manager := NewManager(NewMemoryAdapter())
	_, err := manager.Replay(contract.ReplayParams{SchemaVersion: 1, Generation: 1, Revision: 1, Streams: []contract.StreamSpec{testStream("a")}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Replay(contract.ReplayParams{SchemaVersion: 1, Generation: 2, Revision: 1, Streams: []contract.StreamSpec{testStream("b"), testStream("b")}})
	if !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("invalid replay error = %v", err)
	}
	generation, revision, streams := manager.Snapshot()
	if generation != 1 || revision != 1 || len(streams) != 1 || streams[0].ID != "a" {
		t.Fatalf("replay was not atomic: %d/%d %#v", generation, revision, streams)
	}
}
