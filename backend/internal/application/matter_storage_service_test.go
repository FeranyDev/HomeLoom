package application

import (
	"context"
	"testing"

	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

type matterStorageStub struct {
	value             []byte
	allocationChanges []bool
	allocationCalls   int
}

type matterStorageAuditStub struct {
	*matterStorageStub
	events []domainaudit.Event
}

func (s *matterStorageAuditStub) AppendAuditEvent(_ context.Context, event domainaudit.Event) (domainaudit.Event, error) {
	s.events = append(s.events, event)
	return event, nil
}

func (s *matterStorageStub) PutMatterRuntimeValue(_ context.Context, _, _ string, value []byte) error {
	s.value = value
	return nil
}
func (s *matterStorageStub) GetMatterRuntimeValue(context.Context, string, string) ([]byte, bool, error) {
	return s.value, true, nil
}
func (*matterStorageStub) ListMatterRuntimeValues(context.Context, string) ([]target.MatterRuntimeValue, error) {
	return nil, nil
}
func (*matterStorageStub) DeleteMatterRuntimeValue(context.Context, string, string) error {
	return nil
}
func (*matterStorageStub) ClearMatterRuntimeValues(context.Context, string) error { return nil }
func (s *matterStorageStub) AllocateMatterEndpoint(context.Context, string, string, device.Type) (uint16, bool, error) {
	changed := true
	if s.allocationCalls < len(s.allocationChanges) {
		changed = s.allocationChanges[s.allocationCalls]
	}
	s.allocationCalls++
	return 2, changed, nil
}
func (*matterStorageStub) TombstoneMatterEndpoint(context.Context, string, string) error {
	return nil
}
func (*matterStorageStub) ConfirmMatterEndpointDeviceType(context.Context, string, string, device.Type, bool) error {
	return nil
}
func (*matterStorageStub) MatterEndpointIdentity(context.Context, string, string) (target.MatterEndpointIdentity, bool, error) {
	return target.MatterEndpointIdentity{EndpointID: 2}, true, nil
}
func (*matterStorageStub) ListMatterEndpointIdentities(context.Context, string) ([]target.MatterEndpointIdentity, error) {
	return nil, nil
}

func TestMatterStorageServiceDefensivelyCopiesRuntimeValues(t *testing.T) {
	ctx := context.Background()
	store := &matterStorageStub{}
	service := NewMatterStorageService(store)
	input := []byte("identity")
	if err := service.Put(ctx, "matter-main", "fabric", input); err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	if string(store.value) != "identity" {
		t.Fatal("Put passed a caller-owned buffer to persistence")
	}
	output, found, err := service.Get(ctx, "matter-main", "fabric")
	if err != nil || !found || string(output) != "identity" {
		t.Fatalf("Get = %q, %v, %v", output, found, err)
	}
	output[0] = 'Y'
	if string(store.value) != "identity" {
		t.Fatal("Get returned a persistence-owned buffer")
	}
}

func TestMatterStorageServiceRejectsUnavailableStore(t *testing.T) {
	service := NewMatterStorageService(nil)
	if _, _, err := service.Get(context.Background(), "matter-main", "key"); err == nil {
		t.Fatal("Get accepted an unavailable store")
	}
}

func TestMatterStorageServiceDoesNotAuditRoutineIdentityStorage(t *testing.T) {
	store := &matterStorageAuditStub{matterStorageStub: &matterStorageStub{}}
	service := NewMatterStorageService(store)
	ctx := context.Background()
	if err := service.Put(ctx, "matter-main", "fabric/secret", []byte("credential")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Get(ctx, "matter-main", "fabric/secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(ctx, "matter-main"); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 0 {
		t.Fatalf("routine identity operations wrote audit events: %#v", store.events)
	}
	if err := service.Clear(ctx, "matter-main"); err != nil {
		t.Fatal(err)
	}
	want := []string{"matter.identity.clear"}
	if len(store.events) != len(want) {
		t.Fatalf("audit events = %#v", store.events)
	}
	for index, event := range store.events {
		if event.Action != want[index] || event.ResourceID != "matter-main" || event.ResourceType != "matter-identity" {
			t.Fatalf("audit event %d = %#v", index, event)
		}
		if event.Action == "credential" || event.Route == "credential" {
			t.Fatal("audit event leaked identity value")
		}
	}
}

func TestMatterStorageServiceAuditsOnlyMatterEndpointChanges(t *testing.T) {
	store := &matterStorageAuditStub{matterStorageStub: &matterStorageStub{allocationChanges: []bool{true, false, true}}}
	service := NewMatterStorageService(store)
	ctx := context.Background()
	for range 3 {
		if _, err := service.AllocateEndpoint(ctx, "matter-main", "virtual-switch-1", device.TypeSwitch); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.events) != 2 {
		t.Fatalf("endpoint audit events = %#v", store.events)
	}
	for _, event := range store.events {
		if event.Action != "matter.endpoint.allocate" || len(event.Details) != 2 || event.Details[0].Value != "2" || event.Details[1].Value != "virtual-switch-1" {
			t.Fatalf("endpoint audit event = %#v", event)
		}
	}
}
