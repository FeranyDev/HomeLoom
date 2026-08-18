package subprocesslog

import (
	"strings"
	"testing"
)

func TestStoreCollectsLinesRedactsSecretsAndBoundsHistory(t *testing.T) {
	store := New(2)
	writer := store.Writer("matter", "bridge-main")
	_, _ = writer.Write([]byte("{\"level\":\"INFO\",\"msg\":\"started\"}\npartial"))
	_, _ = writer.Write([]byte(" line token=secret-value\nlast\n"))
	entries := store.Snapshot(0, 10)
	if len(entries) != 2 || !strings.HasPrefix(entries[0].Message, "partial line token=") || entries[1].Message != "last" {
		t.Fatalf("entries = %#v", entries)
	}
	if strings.Contains(entries[0].Message, "secret-value") {
		t.Fatal("secret was not redacted")
	}
}

func TestStoreSnapshotSupportsCursorAndLimit(t *testing.T) {
	store := New(5)
	for _, line := range []string{"one", "two", "three"} {
		store.Append("camera", "stream-1", []byte(line))
	}
	entries := store.Snapshot(1, 1)
	if len(entries) != 1 || entries[0].Message != "three" || entries[0].Sequence != 3 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestStoreClassifiesCameraSubsystems(t *testing.T) {
	store := New(5)
	store.Append("camera-kernel", "camera-1", []byte(`{"level":"info","msg":"pair setup completed","component":"camera-kernel","module":"homekit"}`))
	store.Append("camera-kernel", "camera-1", []byte(`{"level":"info","msg":"decoder ready","component":"camera-kernel","module":"ffmpeg"}`))
	entries := store.Snapshot(0, 5)
	if len(entries) != 2 || entries[0].Subsystem != "homekit" || entries[0].Component != "camera-kernel" || entries[0].Module != "homekit" || entries[1].Subsystem != "ffmpeg" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestStoreSubscriptionIsBoundedAndRecoversWithCursorSnapshot(t *testing.T) {
	store := New(DefaultCapacity)
	updates, unsubscribe := store.Subscribe()
	defer unsubscribe()
	for index := 0; index < SubscriberBuffer+2; index++ {
		store.Append("backend", "main", []byte("line"))
	}
	if len(updates) != SubscriberBuffer {
		t.Fatalf("live subscription length = %d, want %d", len(updates), SubscriberBuffer)
	}
	entries := store.Snapshot(SubscriberBuffer, 10)
	if len(entries) != 2 || entries[0].Sequence != SubscriberBuffer+1 || entries[1].Sequence != SubscriberBuffer+2 {
		t.Fatalf("cursor recovery = %#v", entries)
	}
}

func TestStoreSubscriptionStopsAfterUnsubscribe(t *testing.T) {
	store := New(5)
	updates, unsubscribe := store.Subscribe()
	unsubscribe()
	store.Append("backend", "main", []byte("not delivered"))
	if _, open := <-updates; open {
		t.Fatal("subscription channel remained open after unsubscribe")
	}
}
