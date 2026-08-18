package application_test

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
)

type logicalServiceStore struct {
	items map[string]logicaldevice.Config
}

func (s *logicalServiceStore) ListLogicalDevices(context.Context) ([]logicaldevice.Config, error) {
	result := make([]logicaldevice.Config, 0, len(s.items))
	for _, item := range s.items {
		result = append(result, item.Clone())
	}
	return result, nil
}
func (s *logicalServiceStore) SaveLogicalDevice(_ context.Context, item logicaldevice.Config) error {
	if s.items == nil {
		s.items = map[string]logicaldevice.Config{}
	}
	s.items[item.ID] = item.Clone()
	return nil
}
func (s *logicalServiceStore) DeleteLogicalDevice(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

type logicalServiceRuntime struct{ items []logicaldevice.Config }

func (r *logicalServiceRuntime) SetLogicalDevices(items []logicaldevice.Config) error {
	r.items = append([]logicaldevice.Config(nil), items...)
	return nil
}
func (r *logicalServiceRuntime) LogicalDevices() []logicaldevice.Config {
	return append([]logicaldevice.Config(nil), r.items...)
}
func (*logicalServiceRuntime) LogicalDeviceCandidates(context.Context) ([]logicaldevice.MatchCandidate, error) {
	return nil, nil
}
func (r *logicalServiceRuntime) LogicalDeviceExplanations(id string) ([]logicaldevice.RouteExplanation, bool) {
	return nil, len(r.items) == 1 && r.items[0].ID == id
}

func TestLogicalDeviceServicePersistsAndUnlinksRuntimeConfiguration(t *testing.T) {
	store := &logicalServiceStore{items: map[string]logicaldevice.Config{}}
	runtime := &logicalServiceRuntime{}
	service := application.NewLogicalDeviceService(store, runtime, nil)
	item := logicaldevice.Config{ID: "living-switch", Name: "客厅主灯", Type: device.TypeSwitch, Bindings: []logicaldevice.Binding{{SourceRef: logicaldevice.SourceRef{ProviderID: "local", DeviceID: "switch-local"}}, {SourceRef: logicaldevice.SourceRef{ProviderID: "cloud", DeviceID: "switch-cloud"}, Priority: 10}}}
	if err := service.Save(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 1 || len(runtime.items) != 1 || runtime.items[0].ID != item.ID {
		t.Fatalf("saved store=%#v runtime=%#v", store.items, runtime.items)
	}
	if err := service.Delete(context.Background(), item.ID); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 0 || len(runtime.items) != 0 {
		t.Fatalf("deleted store=%#v runtime=%#v", store.items, runtime.items)
	}
}
