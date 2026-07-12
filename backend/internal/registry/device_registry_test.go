package registry

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func TestRegistryUpsertAndSortedList(t *testing.T) {
	registry := NewDeviceRegistry([]device.Device{{ID: "b", Name: "old"}, {ID: "a"}})
	registry.Upsert(device.Device{ID: "b", Name: "new"})
	registry.Upsert(device.Device{ID: "c"})
	items := registry.List()
	if len(items) != 3 || items[0].ID != "a" || items[1].ID != "b" || items[2].ID != "c" {
		t.Fatalf("List() = %#v", items)
	}
	if items[1].Name != "new" {
		t.Fatalf("upserted name = %q", items[1].Name)
	}
}
