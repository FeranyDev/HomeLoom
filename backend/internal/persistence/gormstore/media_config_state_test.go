package gormstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func testMediaStream(id, deviceID string) MediaStream {
	return MediaStream{
		ID: id, DeviceID: deviceID, Protocol: "rtsp", Profile: "main",
		Mode: "preload", AudioEnabled: true, OptionsJSON: []byte(`{}`), Enabled: true,
	}
}

func TestMediaConfigStateVersionsStreamChangesAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	initial, err := store.GetMediaConfigVersion(ctx)
	if err != nil || initial.Generation != 1 || initial.Revision != 1 {
		t.Fatalf("initial media config version = %#v, %v", initial, err)
	}
	saveTestMediaSource(t, ctx, store, "camera-version")

	saved, err := store.SaveMediaStreamVersioned(ctx, testMediaStream("stream-version", "camera-version"))
	if err != nil || saved.Generation != 1 || saved.Revision != 2 {
		t.Fatalf("saved media config version = %#v, %v", saved, err)
	}
	stream, found, err := store.GetMediaStream(ctx, "stream-version")
	if err != nil || !found || stream.Revision != saved.Revision {
		t.Fatalf("versioned stream = %#v, %v, %v", stream, found, err)
	}

	beforeFailure, err := store.GetMediaConfigVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveMediaStreamVersioned(ctx, testMediaStream("orphan", "missing-camera")); err == nil {
		t.Fatal("orphan media stream was accepted")
	}
	afterFailure, err := store.GetMediaConfigVersion(ctx)
	if err != nil || afterFailure.Generation != beforeFailure.Generation ||
		afterFailure.Revision != beforeFailure.Revision {
		t.Fatalf("failed save changed config version: %#v -> %#v, %v", beforeFailure, afterFailure, err)
	}
	if _, err := store.DeleteMediaStreamVersioned(ctx, "missing-stream"); err == nil {
		t.Fatal("missing media stream deletion was accepted")
	}
	afterMissingDelete, err := store.GetMediaConfigVersion(ctx)
	if err != nil || afterMissingDelete.Revision != beforeFailure.Revision {
		t.Fatalf("failed delete changed config version: %#v, %v", afterMissingDelete, err)
	}

	deleted, err := store.DeleteMediaStreamVersioned(ctx, "stream-version")
	if err != nil || deleted.Generation != 1 || deleted.Revision != 3 {
		t.Fatalf("deleted media config version = %#v, %v", deleted, err)
	}
	if _, found, err := store.GetMediaStream(ctx, "stream-version"); err != nil || found {
		t.Fatalf("deleted stream = %v, %v", found, err)
	}

	generation, err := store.BumpMediaConfigGeneration(ctx)
	if err != nil || generation.Generation != 2 || generation.Revision != 1 {
		t.Fatalf("new media config generation = %#v, %v", generation, err)
	}
}

func TestMediaConfigStateCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	expected, err := store.GetMediaConfigVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next := MediaConfigVersion{Generation: expected.Generation, Revision: expected.Revision + 1}
	swapped, err := store.CompareAndSwapMediaConfigVersion(ctx, expected, next)
	if err != nil || !swapped {
		t.Fatalf("revision CAS = %v, %v", swapped, err)
	}
	if swapped, err := store.CompareAndSwapMediaConfigVersion(ctx, expected, next); err != nil || swapped {
		t.Fatalf("stale revision CAS = %v, %v", swapped, err)
	}
	current, err := store.GetMediaConfigVersion(ctx)
	if err != nil || current.Generation != next.Generation || current.Revision != next.Revision {
		t.Fatalf("current CAS version = %#v, %v", current, err)
	}
	generation := MediaConfigVersion{Generation: current.Generation + 1, Revision: 1}
	if swapped, err := store.CompareAndSwapMediaConfigVersion(ctx, current, generation); err != nil || !swapped {
		t.Fatalf("generation CAS = %v, %v", swapped, err)
	}
	if _, err := store.CompareAndSwapMediaConfigVersion(ctx, generation, MediaConfigVersion{
		Generation: generation.Generation + 1,
		Revision:   2,
	}); err == nil {
		t.Fatal("invalid generation reset CAS was accepted")
	}
}

func TestMediaConfigStateSingletonConstraintsAcrossConfiguredDialect(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	second := mediaConfigStateRow{ID: 2, Generation: 1, Revision: 1, UpdatedAt: 1}
	if err := store.orm.WithContext(ctx).Create(&second).Error; err == nil {
		t.Fatalf("second singleton was accepted by %s", store.databaseKind)
	}
	if err := store.orm.WithContext(ctx).Model(&mediaConfigStateRow{}).
		Where("id = ?", mediaConfigStateID).Update("generation", 0).Error; err == nil {
		t.Fatalf("zero generation was accepted by %s", store.databaseKind)
	}
	if err := store.orm.WithContext(ctx).Model(&mediaConfigStateRow{}).
		Where("id = ?", mediaConfigStateID).Update("revision", 0).Error; err == nil {
		t.Fatalf("zero revision was accepted by %s", store.databaseKind)
	}
}

func TestConcurrentMediaStreamWritesReceiveUniqueRevisions(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveTestMediaSource(t, ctx, store, "camera-concurrent")

	const writers = 12
	type result struct {
		id      string
		version MediaConfigVersion
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, writers)
	var group sync.WaitGroup
	for index := 0; index < writers; index++ {
		id := fmt.Sprintf("stream-concurrent-%02d", index)
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			version, err := store.SaveMediaStreamVersioned(ctx, testMediaStream(id, "camera-concurrent"))
			results <- result{id: id, version: version, err: err}
		}()
	}
	close(start)
	group.Wait()
	close(results)

	revisions := make(map[uint64]string, writers)
	for item := range results {
		if item.err != nil {
			t.Fatalf("concurrent save %q: %v", item.id, item.err)
		}
		if previous, duplicate := revisions[item.version.Revision]; duplicate {
			t.Fatalf("streams %q and %q received revision %d", previous, item.id, item.version.Revision)
		}
		revisions[item.version.Revision] = item.id
		stream, found, err := store.GetMediaStream(ctx, item.id)
		if err != nil || !found || stream.Revision != item.version.Revision {
			t.Fatalf("stream %q revision = %#v, %v, %v", item.id, stream, found, err)
		}
	}
	current, err := store.GetMediaConfigVersion(ctx)
	if err != nil || current.Generation != 1 || current.Revision != 1+writers {
		t.Fatalf("concurrent final version = %#v, %v", current, err)
	}
}
