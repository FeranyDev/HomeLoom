package postgres

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func TestProviderMappingSourceFansOutWhileModelTargetRemainsUnique(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := mapping.Binding{ID: "binding-one", ProfileID: "builtin-active-low", ProviderID: "virtual-main", DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "vendor", PropertyID: "raw-power", ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power", Enabled: true}
	if err := store.SaveMappingBinding(ctx, item); err != nil {
		t.Fatal(err)
	}
	second := item
	second.ID, second.ModelCapabilityID, second.ModelPropertyID = "binding-two", "aux", "mirrored-power"
	if err := store.SaveMappingBinding(ctx, second); err != nil {
		t.Fatalf("same Provider source could not fan out: %v", err)
	}
	duplicateTarget := item
	duplicateTarget.ID, duplicateTarget.PropertyID = "binding-three", "other-raw-power"
	if err := store.SaveMappingBinding(ctx, duplicateTarget); err == nil {
		t.Fatal("duplicate unified-model target was accepted")
	}
	items, err := store.ListMappingBindings(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("bindings = %#v, error = %v", items, err)
	}
	item.Enabled = false
	if err := store.SaveMappingBinding(ctx, item); err != nil {
		t.Fatal(err)
	}
	items, _ = store.ListMappingBindings(ctx)
	for _, current := range items {
		if current.ID == item.ID && current.Enabled {
			t.Fatal("binding update was not persisted")
		}
	}
	if err := store.DeleteMappingBinding(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMappingBinding(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerMappingUniquenessIsScopedPerDevice(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := mapping.Binding{ID: "consumer-one", Stage: mapping.StageConsumer, ProviderID: "virtual-main", DeviceID: "switch-one", DeviceType: "switch", ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power", ConsumerID: "homekit", ConsumerProperty: "Switch.On", Enabled: true}
	if err := store.SaveMappingBinding(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.DeviceID = "consumer-two", "switch-two"
	if err := store.SaveMappingBinding(ctx, second); err != nil {
		t.Fatalf("same Consumer property on another device was rejected: %v", err)
	}
	duplicate := first
	duplicate.ID = "consumer-duplicate"
	if err := store.SaveMappingBinding(ctx, duplicate); err == nil {
		t.Fatal("duplicate Consumer property on the same device was accepted")
	}
}

func TestConsumerMappingUniquenessIsScopedPerTargetVirtualDevice(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := mapping.Binding{
		ID: "target-consumer-one", Stage: mapping.StageConsumer,
		ProviderID: "virtual-main", DeviceID: "switch-one", DeviceType: "switch",
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power",
		TargetID: "apple-main", ConsumerDeviceID: "living-room-switch",
		ConsumerID: "homekit", ConsumerProperty: "Switch.On", Enabled: true,
	}
	if err := store.SaveMappingBinding(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.TargetID, second.ConsumerDeviceID = "target-consumer-two", "apple-guest", "guest-switch"
	if err := store.SaveMappingBinding(ctx, second); err != nil {
		t.Fatalf("same source and characteristic on another virtual device was rejected: %v", err)
	}
	items, err := store.ListMappingBindings(ctx)
	if err != nil || len(items) != 2 || items[0].TargetID == "" || items[1].ConsumerDeviceID == "" {
		t.Fatalf("scoped bindings = %#v, error = %v", items, err)
	}
	duplicate := first
	duplicate.ID = "target-consumer-duplicate"
	if err := store.SaveMappingBinding(ctx, duplicate); err == nil {
		t.Fatal("duplicate Consumer property on the same target virtual device was accepted")
	}
}
