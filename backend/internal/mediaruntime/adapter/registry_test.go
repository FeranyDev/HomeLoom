package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
)

func spec(id string) contract.StreamSpec {
	return contract.StreamSpec{SchemaVersion: 1, ID: id, DeviceID: "device-" + id, Protocol: "rtsp", Profile: "main", Mode: "on_demand"}
}

func TestRegistryIsCaseInsensitiveAndRejectsDuplicates(t *testing.T) {
	registry := NewRegistry()
	producer := &fakeProducer{}
	if err := registry.Register(" RTSP ", producer); err != nil {
		t.Fatal(err)
	}
	got, err := registry.Producer("rtsp")
	if err != nil || got != producer {
		t.Fatalf("producer = %v, %v", got, err)
	}
	if err := registry.Register("rtsp", producer); err == nil {
		t.Fatal("expected duplicate registration error")
	}
	if _, err := registry.Producer("onvif"); !errors.Is(err, ErrProducerNotFound) {
		t.Fatalf("missing producer error = %v", err)
	}
}

func TestLifecycleAdapterStartsReplacesAndStopsWithoutRetainingSource(t *testing.T) {
	registry := NewRegistry()
	producer := &fakeProducer{}
	if err := registry.Register("rtsp", producer); err != nil {
		t.Fatal(err)
	}
	resolves := 0
	adapter, err := NewLifecycleAdapter(registry, func(_ context.Context, input contract.StreamSpec) (Source, error) {
		resolves++
		return Source{URI: "rtsp://ephemeral.example/" + input.ID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first := spec("front")
	if err := adapter.Upsert(first); err != nil {
		t.Fatal(err)
	}
	if producer.starts != 1 || resolves != 1 {
		t.Fatalf("start=%d resolves=%d", producer.starts, resolves)
	}
	if err := adapter.Upsert(first); err != nil {
		t.Fatal(err)
	}
	if producer.starts != 1 || resolves != 1 {
		t.Fatal("unchanged stream restarted")
	}
	updated := first
	updated.Options = []byte(`{"transport":"tcp"}`)
	if err := adapter.Upsert(updated); err != nil {
		t.Fatal(err)
	}
	if producer.starts != 2 || producer.closed != 1 {
		t.Fatalf("replace starts=%d closes=%d", producer.starts, producer.closed)
	}
	if err := adapter.Delete("front"); err != nil {
		t.Fatal(err)
	}
	if producer.closed != 2 {
		t.Fatalf("closes=%d", producer.closed)
	}
}

func TestLifecycleAdapterReleasesExistingSessionBeforeStartingReplacement(t *testing.T) {
	registry := NewRegistry()
	producer := &exclusiveProducer{}
	if err := registry.Register("rtsp", producer); err != nil {
		t.Fatal(err)
	}
	adapter, err := NewLifecycleAdapter(registry, func(_ context.Context, input contract.StreamSpec) (Source, error) {
		return Source{URI: input.ID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first := spec("front")
	if err := adapter.Upsert(first); err != nil {
		t.Fatal(err)
	}
	updated := first
	updated.Options = []byte(`{"publish_homekit":true}`)
	if err := adapter.Upsert(updated); err != nil {
		t.Fatalf("replacement failed while the old session owned its listener: %v", err)
	}
	if producer.starts != 2 || producer.closes != 1 || !producer.active {
		t.Fatalf("starts=%d closes=%d active=%v", producer.starts, producer.closes, producer.active)
	}
}

func TestLifecycleAdapterRestoresExistingSessionWhenReplacementFails(t *testing.T) {
	registry := NewRegistry()
	producer := &exclusiveProducer{}
	if err := registry.Register("rtsp", producer); err != nil {
		t.Fatal(err)
	}
	adapter, err := NewLifecycleAdapter(registry, func(_ context.Context, input contract.StreamSpec) (Source, error) {
		return Source{URI: input.ID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first := spec("front")
	if err := adapter.Upsert(first); err != nil {
		t.Fatal(err)
	}
	producer.failNext = true
	updated := first
	updated.Options = []byte(`{"publish_homekit":true}`)
	if err := adapter.Upsert(updated); err == nil {
		t.Fatal("expected replacement failure")
	}
	if producer.starts != 3 || producer.closes != 1 || !producer.active {
		t.Fatalf("rollback starts=%d closes=%d active=%v", producer.starts, producer.closes, producer.active)
	}
	if err := adapter.Delete(first.ID); err != nil {
		t.Fatalf("restored session was not retained: %v", err)
	}
	if producer.closes != 2 || producer.active {
		t.Fatalf("restored session closes=%d active=%v", producer.closes, producer.active)
	}
}

func TestLifecycleAdapterReplaceDoesNotTearDownExistingOnStartFailure(t *testing.T) {
	registry := NewRegistry()
	producer := &fakeProducer{failFor: "bad"}
	if err := registry.Register("rtsp", producer); err != nil {
		t.Fatal(err)
	}
	adapter, err := NewLifecycleAdapter(registry, func(_ context.Context, input contract.StreamSpec) (Source, error) { return Source{URI: input.ID}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Upsert(spec("good")); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Replace([]contract.StreamSpec{spec("bad")}); err == nil {
		t.Fatal("expected start error")
	}
	if producer.closed != 0 {
		t.Fatalf("existing session was closed: %d", producer.closed)
	}
	if err := adapter.Delete("good"); err != nil {
		t.Fatal(err)
	}
	if producer.closed != 1 {
		t.Fatalf("existing session not retained: %d", producer.closed)
	}
}

func TestLifecycleAdapterReplaceReleasesChangedSessionBeforeRestart(t *testing.T) {
	registry := NewRegistry()
	producer := &exclusiveProducer{}
	if err := registry.Register("rtsp", producer); err != nil {
		t.Fatal(err)
	}
	adapter, err := NewLifecycleAdapter(registry, func(_ context.Context, input contract.StreamSpec) (Source, error) {
		return Source{URI: input.ID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first := spec("front")
	if err := adapter.Replace([]contract.StreamSpec{first}); err != nil {
		t.Fatal(err)
	}
	updated := first
	updated.Options = []byte(`{"publish_homekit":true}`)
	if err := adapter.Replace([]contract.StreamSpec{updated}); err != nil {
		t.Fatalf("replace failed while the old session owned its listener: %v", err)
	}
	if producer.starts != 2 || producer.closes != 1 || !producer.active {
		t.Fatalf("starts=%d closes=%d active=%v", producer.starts, producer.closes, producer.active)
	}
}

type fakeProducer struct {
	starts, closed int
	failFor        string
}

func (p *fakeProducer) Start(_ context.Context, input contract.StreamSpec, source Source) (Session, error) {
	if source.URI == "" {
		return nil, errors.New("missing source")
	}
	if input.ID == p.failFor {
		return nil, errors.New("start failed")
	}
	p.starts++
	return fakeSession{onClose: func() { p.closed++ }}, nil
}

type fakeSession struct{ onClose func() }

func (s fakeSession) Close(context.Context) error { s.onClose(); return nil }

type exclusiveProducer struct {
	starts, closes int
	active         bool
	failNext       bool
}

func (p *exclusiveProducer) Start(context.Context, contract.StreamSpec, Source) (Session, error) {
	if p.active {
		return nil, errors.New("listener already in use")
	}
	p.starts++
	if p.failNext {
		p.failNext = false
		return nil, errors.New("start failed")
	}
	p.active = true
	return fakeSession{onClose: func() {
		p.closes++
		p.active = false
	}}, nil
}
