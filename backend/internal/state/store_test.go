package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
)

func TestApplyWithResultExplainsCrossProviderPriority(t *testing.T) {
	key := domainstate.Key{DeviceID: "lamp", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}
	now := time.Now().UTC()
	store := NewStore(Options{PriorityResolver: StaticPriorityResolver{
		Providers: map[string]int{"cloud": 10, "lan": 1},
	}})
	if _, applied := store.Apply(domainstate.StateValue{Key: key, ProviderID: "lan", Value: domainstate.BoolValue(true), ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityReported}); !applied {
		t.Fatal("initial value was not applied")
	}
	value, result := store.ApplyWithResult(domainstate.StateValue{Key: key, ProviderID: "cloud", Value: domainstate.BoolValue(false), ObservedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-time.Minute), Quality: domainstate.QualityReported})
	if !result.Applied || result.Decision != DecisionAccepted || result.CurrentPriority != 1 || result.IncomingPriority != 10 || value.ProviderID != "cloud" {
		t.Fatalf("value = %#v, result = %#v", value, result)
	}
	value, result = store.ApplyWithResult(domainstate.StateValue{Key: key, ProviderID: "lan", Value: domainstate.BoolValue(true), ObservedAt: now.Add(time.Minute), ReceivedAt: now.Add(time.Minute), Quality: domainstate.QualityReported})
	if result.Applied || result.Decision != DecisionRejectedProviderPriority || result.CurrentPriority != 10 || result.IncomingPriority != 1 || value.ProviderID != "cloud" || value.Value.Bool == nil || *value.Value.Bool {
		t.Fatalf("lower priority value = %#v, result = %#v", value, result)
	}
}

func TestPropertyPriorityOverridesProviderDefault(t *testing.T) {
	key := domainstate.Key{DeviceID: "sensor", EndpointID: "main", CapabilityID: "air-quality", PropertyID: "pm25"}
	now := time.Now().UTC()
	store := NewStore(Options{PriorityResolver: StaticPriorityResolver{
		Providers: map[string]int{"cloud": 10, "lan": 1},
		Properties: map[domainstate.Key]map[string]int{
			key: {"cloud": 1, "lan": 20},
		},
	}})
	store.Apply(domainstate.StateValue{Key: key, ProviderID: "cloud", Value: domainstate.IntValue(30), ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityReported})
	value, result := store.ApplyWithResult(domainstate.StateValue{Key: key, ProviderID: "lan", Value: domainstate.IntValue(20), ObservedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-time.Minute), Quality: domainstate.QualityReported})
	if !result.Applied || value.ProviderID != "lan" || value.Value.Int == nil || *value.Value.Int != 20 {
		t.Fatalf("value = %#v, result = %#v", value, result)
	}
}

func TestCheckpointRoundTripForcesRestoredValuesStale(t *testing.T) {
	key := domainstate.Key{DeviceID: "lamp", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}
	now := time.Now().UTC()
	store := NewStore()
	store.Apply(domainstate.StateValue{Key: key, ProviderID: "lan", Value: domainstate.BoolValue(true), ObservedAt: now, ReceivedAt: now, ExpiresAt: now.Add(time.Minute), Quality: domainstate.QualityReported, PendingCommandID: "cmd-1"})
	payload, err := json.Marshal(store.Checkpoint())
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		t.Fatal(err)
	}
	restored := NewStore()
	if !restored.RestoreCheckpoint(checkpoint) {
		t.Fatal("RestoreCheckpoint() rejected a current checkpoint")
	}
	value, exists := restored.Get(key)
	if !exists || value.Quality != domainstate.QualityStale || value.Source != domainstate.SourcePersistentCache || value.Available || value.UnavailableReason != domainstate.UnavailableStale || value.PendingCommandID != "" || !value.ExpiresAt.IsZero() || value.Value.Bool == nil || !*value.Value.Bool {
		t.Fatalf("restored value = %#v", value)
	}
	if restored.RestoreCheckpoint(Checkpoint{Version: CheckpointVersion + 1}) {
		t.Fatal("RestoreCheckpoint() accepted an unsupported version")
	}
}

func TestUnknownNullAndUnavailableRetainsLastValue(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	key := domainstate.Key{DeviceID: "sensor", EndpointID: "main", CapabilityID: "mode", PropertyID: "value"}
	unknown, created := store.EnsureUnknown(domainstate.StateValue{Key: key, ProviderID: "provider", Source: domainstate.SourceUnknown, ObservedAt: now, ReceivedAt: now, UnavailableReason: domainstate.UnavailableAvailabilityUnknown})
	payload, err := json.Marshal(unknown)
	if err != nil || !created || unknown.Known || unknown.Available || unknown.Quality != domainstate.QualityUnknown || !strings.Contains(string(payload), `"value":null`) || !strings.Contains(string(payload), `"unavailableReason":"availability-unknown"`) {
		t.Fatalf("unknown = %#v, json = %s, error = %v", unknown, payload, err)
	}
	reported, accepted := store.Apply(domainstate.StateValue{Key: key, Value: domainstate.EnumValue("auto"), ProviderID: "provider", Source: domainstate.SourceReported, ObservedAt: now.Add(time.Second), ReceivedAt: now.Add(time.Second), Quality: domainstate.QualityReported})
	if !accepted || !reported.Known || !reported.Available || reported.Value.Kind != domainstate.KindEnum || reported.Value.String == nil || *reported.Value.String != "auto" {
		t.Fatalf("reported = %#v", reported)
	}
	changed := store.MarkDeviceUnavailable("sensor", domainstate.UnavailableDeviceOffline)
	payload, err = json.Marshal(changed[0])
	if err != nil || len(changed) != 1 || !changed[0].Known || changed[0].Available || changed[0].Quality != domainstate.QualityStale || changed[0].Value.String == nil || *changed[0].Value.String != "auto" || !strings.Contains(string(payload), `"value":{"kind":"enum","string":"auto"}`) {
		t.Fatalf("unavailable = %#v, json = %s, error = %v", changed, payload, err)
	}
}

func TestMarkCapabilityUnavailableDoesNotAffectMedia(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	for _, capability := range []string{"media", "privacy"} {
		store.Apply(domainstate.StateValue{
			Key:   domainstate.Key{DeviceID: "camera", EndpointID: "main", CapabilityID: capability, PropertyID: "enabled"},
			Value: domainstate.BoolValue(true), ProviderID: "camera-provider", Source: domainstate.SourceReported,
			ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityReported,
		})
	}
	changed := store.MarkCapabilityUnavailable(
		"camera", "main", "privacy", domainstate.UnavailableControlProviderOffline,
	)
	if len(changed) != 1 || changed[0].Key.CapabilityID != "privacy" ||
		changed[0].Available || changed[0].UnavailableReason != domainstate.UnavailableControlProviderOffline {
		t.Fatalf("changed states = %#v", changed)
	}
	media, _ := store.Get(domainstate.Key{DeviceID: "camera", EndpointID: "main", CapabilityID: "media", PropertyID: "enabled"})
	if !media.Available || media.UnavailableReason != "" {
		t.Fatalf("media state was degraded = %#v", media)
	}
}

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

func TestApplyRejectsDuplicateSequenceDespiteNewerTimestamp(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	key := domainstate.Key{DeviceID: "device", PropertyID: "power"}
	first, accepted := store.Apply(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(true), ProviderID: "provider", Sequence: 7, ObservedAt: now, ReceivedAt: now, Quality: domainstate.QualityReported})
	if !accepted {
		t.Fatal("first sequence was rejected")
	}
	current, accepted := store.Apply(domainstate.StateValue{Key: key, Value: domainstate.BoolValue(false), ProviderID: "provider", Sequence: 7, ObservedAt: now.Add(time.Minute), ReceivedAt: now.Add(time.Minute), Quality: domainstate.QualityConfirmed})
	if accepted || current.Version != first.Version || current.Value.Bool == nil || !*current.Value.Bool {
		t.Fatalf("duplicate sequence accepted: %#v", current)
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
