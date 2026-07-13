package state

import (
	"fmt"
	"testing"
	"time"

	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
)

func TestApplyRejectsOlderSequenceFromSameProvider(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	key := domainstate.Key{DeviceID: "switch-1", PropertyID: "power"}
	store.Apply(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(true), ProviderID: "mqtt", Sequence: 2, ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityReported})
	current, accepted := store.Apply(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(false), ProviderID: "mqtt", Sequence: 1, ObservedAt: now.Add(time.Second), ReceivedAt: now.Add(time.Second), Quality: domainstate.QualityConfirmed})
	if accepted {
		t.Fatal("older provider sequence was accepted")
	}
	if current.Value.Bool == nil || !*current.Value.Bool {
		t.Fatal("current state was replaced")
	}
}

func BenchmarkStoreReadFiveHundredDevices(b *testing.B) {
	store := NewStore()
	now := time.Now().UTC()
	for index := 0; index < 500; index++ {
		deviceID := fmt.Sprintf("device-%03d", index)
		store.Apply(domainstate.StateValue{
			Key:   domainstate.Key{DeviceID: deviceID, EndpointID: "main", CapabilityID: "switch", PropertyID: "power"},
			Value: domainstate.BoolValue(true), ProviderID: "benchmark", ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityReported,
		})
	}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		for index := 0; index < 500; index++ {
			if values := store.Device(fmt.Sprintf("device-%03d", index)); len(values) != 1 {
				b.Fatalf("device %d state count = %d", index, len(values))
			}
		}
	}
}

func TestApplyUsesObservedTimeThenQuality(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	key := domainstate.Key{DeviceID: "sensor-1", PropertyID: "temperature"}
	first, accepted := store.Apply(domainstate.StateValue{Key: key, Value: domainstate.NumberValue(20), ProviderID: "a", ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityPolled})
	if !accepted || first.Version != 1 {
		t.Fatal("first state was not accepted with version 1")
	}
	second, accepted := store.Apply(domainstate.StateValue{Key: key, Value: domainstate.NumberValue(21), ProviderID: "b", ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityReported})
	if !accepted || second.Version != 2 || second.Value.Number == nil || *second.Value.Number != 21 {
		t.Fatalf("better state = %#v", second)
	}
}

func TestApplyUsesReceivedTimeAndListsSortedProperties(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	for _, property := range []string{"temperature", "power"} {
		store.Apply(domainstate.StateValue{Key: domainstate.Key{DeviceID: "device", PropertyID: property}, Value: domainstate.BoolValue(true), ProviderID: "p", ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityUnknown})
	}
	key := domainstate.Key{DeviceID: "device", PropertyID: "power"}
	updated, accepted := store.Apply(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(false), ProviderID: "other", ObservedAt: now, ReceivedAt: now.Add(time.Second), Quality: domainstate.QualityUnknown})
	if !accepted || updated.Value.Bool == nil || *updated.Value.Bool {
		t.Fatalf("updated = %#v", updated)
	}
	items := store.Device("device")
	if len(items) != 2 || items[0].Key.PropertyID != "power" || items[1].Key.PropertyID != "temperature" {
		t.Fatalf("Device() = %#v", items)
	}
}

func TestApplyRejectsOlderObservedTime(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	key := domainstate.Key{DeviceID: "device", PropertyID: "power"}
	store.Apply(domainstate.StateValue{Key: key, ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityStale})
	_, accepted := store.Apply(domainstate.StateValue{Key: key, ObservedAt: now.Add(-time.Second), ReceivedAt: now.Add(time.Second), Quality: domainstate.QualityConfirmed})
	if accepted {
		t.Fatal("older observed state was accepted")
	}
}

func TestMarkStaleExpiresOnceAndIncrementsVersion(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	key := domainstate.Key{DeviceID: "device", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}
	store.Apply(domainstate.StateValue{
		Key: key, Value: domainstate.BoolValue(true), ProviderID: "provider",
		ObservedAt: now, ReceivedAt: now, ExpiresAt: now.Add(time.Second), Quality: domainstate.QualityReported,
	})
	if changed := store.MarkStale(now.Add(500 * time.Millisecond)); len(changed) != 0 {
		t.Fatalf("early stale changes = %#v", changed)
	}
	changed := store.MarkStale(now.Add(time.Second))
	if len(changed) != 1 || changed[0].Quality != domainstate.QualityStale || changed[0].Version != 2 {
		t.Fatalf("stale changes = %#v", changed)
	}
	if changed := store.MarkStale(now.Add(2 * time.Second)); len(changed) != 0 {
		t.Fatalf("duplicate stale changes = %#v", changed)
	}
}

func TestMarkDeviceStaleIsImmediateAndIdempotent(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	for _, deviceID := range []string{"offline", "online"} {
		store.Apply(domainstate.StateValue{Key: domainstate.Key{DeviceID: deviceID, PropertyID: "power"}, Value: domainstate.BoolValue(true), ProviderID: "provider", ObservedAt: now, ReceivedAt: now, ExpiresAt: now.Add(time.Hour), Quality: domainstate.QualityReported})
	}
	changed := store.MarkDeviceStale("offline")
	if len(changed) != 1 || changed[0].Quality != domainstate.QualityStale || changed[0].Version != 2 {
		t.Fatalf("changed = %#v", changed)
	}
	if repeated := store.MarkDeviceStale("offline"); len(repeated) != 0 {
		t.Fatalf("repeated = %#v", repeated)
	}
	if current := store.Device("online"); len(current) != 1 || current[0].Quality != domainstate.QualityReported {
		t.Fatalf("unrelated state = %#v", current)
	}
}

func TestOptimisticStateIsOverriddenByAuthoritativeReport(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	key := domainstate.Key{DeviceID: "switch", PropertyID: "power"}
	store.Apply(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(false), ProviderID: "provider", ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityReported})
	optimistic := store.ApplyOptimistic(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(true), ProviderID: "provider", ObservedAt: now.Add(time.Second), ReceivedAt: now.Add(time.Second), ExpiresAt: now.Add(5 * time.Second), Quality: domainstate.QualityOptimistic, PendingCommandID: "command"})
	if optimistic.Version != 2 || optimistic.PendingCommandID == "" {
		t.Fatalf("optimistic = %#v", optimistic)
	}
	reported, accepted := store.Apply(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(true), ProviderID: "provider", ObservedAt: now.Add(-time.Second), ReceivedAt: now.Add(2 * time.Second), Quality: domainstate.QualityReported})
	if !accepted || reported.Quality != domainstate.QualityReported || reported.PendingCommandID != "" || reported.Version != 3 {
		t.Fatalf("reported = %#v", reported)
	}
}

func TestResolveOptimisticRollsBackAndExpiryClearsPendingCommand(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	key := domainstate.Key{DeviceID: "switch", PropertyID: "power"}
	base, _ := store.Apply(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(false), ProviderID: "provider", ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityReported})
	store.ApplyOptimistic(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(true), ProviderID: "provider", ObservedAt: now, ReceivedAt: now, ExpiresAt: now.Add(time.Second), Quality: domainstate.QualityOptimistic, PendingCommandID: "rejected"})
	restored, changed := store.ResolveOptimistic("rejected", &base)
	if !changed || restored.Quality != domainstate.QualityReported || restored.Value.Bool == nil || *restored.Value.Bool {
		t.Fatalf("restored = %#v", restored)
	}
	store.ApplyOptimistic(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(true), ProviderID: "provider", ObservedAt: now, ReceivedAt: now, ExpiresAt: now.Add(time.Second), Quality: domainstate.QualityOptimistic, PendingCommandID: "timeout"})
	expired := store.MarkStale(now.Add(time.Second))
	if len(expired) != 1 || expired[0].PendingCommandID != "" || expired[0].Quality != domainstate.QualityStale {
		t.Fatalf("expired = %#v", expired)
	}
}
