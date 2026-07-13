package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

type targetStoreStub struct {
	saved     []target.Config
	deleted   []string
	saveErr   error
	deleteErr error
}

type targetRuntimeStub struct {
	applied []target.Config
	reset   []target.Config
}

func (s *targetRuntimeStub) Apply(_ context.Context, item target.Config) (TargetRegistration, error) {
	s.applied = append(s.applied, item)
	return TargetRegistration{Info: TargetInfo{ID: item.ID, Type: item.Type, Name: item.Name, Enabled: item.Enabled, Status: "running", Address: item.Address, SetupID: item.SetupID, PairingCode: item.Pin, DeviceIDs: item.DeviceIDs}, QR: []byte("qr")}, nil
}
func (s *targetRuntimeStub) Remove(context.Context, string) error { return nil }
func (s *targetRuntimeStub) ResetPairing(_ context.Context, item target.Config) (TargetRegistration, error) {
	s.reset = append(s.reset, item)
	return TargetRegistration{Info: TargetInfo{ID: item.ID, Type: item.Type, Name: item.Name, Enabled: item.Enabled, Status: "running", Address: item.Address, SetupID: item.SetupID, PairingCode: item.Pin, DeviceIDs: item.DeviceIDs}, QR: []byte("new-qr")}, nil
}

func (s *targetStoreStub) SaveTarget(_ context.Context, item target.Config) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, item)
	return nil
}
func (s *targetStoreStub) DeleteTarget(_ context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, id)
	return nil
}

func TestTargetSaveGeneratesOptionalFields(t *testing.T) {
	store := &targetStoreStub{}
	service := NewTargetService(nil, store)
	info, err := service.Save(context.Background(), target.Config{Type: "apple-hap", Enabled: true})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if info.ID == "" || info.Name == "" || info.Address == "" || info.SetupID == "" || info.PairingCode == "" {
		t.Fatalf("generated target info = %#v", info)
	}
	if len(store.saved) != 1 || store.saved[0].StorePath != "data/hap/"+info.ID {
		t.Fatalf("stored target = %#v", store.saved)
	}
}

func TestTargetServiceListQRStatusAndDelete(t *testing.T) {
	store := &targetStoreStub{}
	service := NewTargetService([]TargetRegistration{{Info: TargetInfo{ID: "one", Status: "starting"}, QR: []byte("qr")}}, store)
	statusEvents := make(chan TargetInfo, 1)
	unsubscribe := service.Subscribe(func(info TargetInfo) { statusEvents <- info })
	defer unsubscribe()
	service.SetStatus("one", "running")
	select {
	case event := <-statusEvents:
		if event.ID != "one" || event.Status != "running" {
			t.Fatalf("status event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("status event was not published")
	}
	items := service.List()
	if len(items) != 1 || items[0].Status != "running" {
		t.Fatalf("List() = %#v", items)
	}
	qr, err := service.QR("one")
	if err != nil || string(qr) != "qr" {
		t.Fatalf("QR() = %q, %v", qr, err)
	}
	qr[0] = 'x'
	again, _ := service.QR("one")
	if string(again) != "qr" {
		t.Fatal("QR() did not return a defensive copy")
	}
	if _, err := service.QR("missing"); err == nil {
		t.Fatal("QR() accepted missing target")
	}
	if err := service.Delete(context.Background(), "one"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if len(service.List()) != 0 || len(store.deleted) != 1 {
		t.Fatal("target was not deleted")
	}
}

func TestTargetSaveRejectsInvalidConfiguration(t *testing.T) {
	service := NewTargetService(nil, &targetStoreStub{})
	cases := []target.Config{
		{ID: "bad/id", Type: "apple-hap"},
		{ID: "id", Type: "unsupported", Name: "name"},
		{ID: "id", Type: "apple-hap", Name: "name", Pin: "abc", Address: ":1", SetupID: "ABCD"},
	}
	for _, item := range cases {
		if _, err := service.Save(context.Background(), item); err == nil {
			t.Fatalf("Save() accepted %#v", item)
		}
	}
}

func TestTargetStoreFailureLeavesPublishedConfigurationUnchanged(t *testing.T) {
	oldConfig := target.Config{ID: "one", Type: "apple-hap", Name: "Original", Enabled: false, Address: ":51826", Pin: "12345678", SetupID: "OLD1", StorePath: "data/hap/one"}
	store := &targetStoreStub{saveErr: errors.New("database unavailable")}
	service := NewTargetService([]TargetRegistration{{Info: TargetInfo{ID: oldConfig.ID, Type: oldConfig.Type, Name: oldConfig.Name, Status: "disabled", Address: oldConfig.Address, SetupID: oldConfig.SetupID}}}, store, oldConfig)
	updated := oldConfig
	updated.Name = "Updated"
	if _, err := service.Save(context.Background(), updated); err == nil {
		t.Fatal("Save() accepted a failed persistent write")
	}
	items := service.List()
	if len(items) != 1 || items[0].Name != oldConfig.Name || items[0].SetupID != oldConfig.SetupID {
		t.Fatalf("published target changed after failed save: %#v", items)
	}
	store.deleteErr = errors.New("database unavailable")
	if err := service.Delete(context.Background(), oldConfig.ID); err == nil {
		t.Fatal("Delete() accepted a failed persistent write")
	}
	if items := service.List(); len(items) != 1 || items[0].ID != oldConfig.ID {
		t.Fatalf("published target was removed after failed delete: %#v", items)
	}
}

func TestTargetPairingMaintenancePreservesConfiguration(t *testing.T) {
	ctx := context.Background()
	config := target.Config{ID: "apple-main", Type: "apple-hap", Name: "Main", Enabled: true, Address: ":51826", Pin: "12345678", SetupID: "OLD1", StorePath: "data/hap/apple-main", DeviceIDs: []string{"switch-1"}}
	store := &targetStoreStub{}
	runtime := &targetRuntimeStub{}
	service := NewTargetService([]TargetRegistration{{Info: TargetInfo{ID: config.ID, Type: config.Type, Name: config.Name, Status: "running"}}}, store, config)
	service.SetRuntime(runtime)
	regenerated, err := service.RegeneratePairing(ctx, config.ID)
	if err != nil {
		t.Fatal(err)
	}
	if regenerated.SetupID == config.SetupID || len(store.saved) != 1 || store.saved[0].Pin == config.Pin || store.saved[0].Address != config.Address || len(store.saved[0].DeviceIDs) != 1 {
		t.Fatalf("regenerated = %#v, saved = %#v", regenerated, store.saved)
	}
	cleared, err := service.ClearPairingIdentity(ctx, config.ID)
	if err != nil || cleared.Status != "running" || len(runtime.reset) != 1 || runtime.reset[0].SetupID != regenerated.SetupID {
		t.Fatalf("cleared = %#v, reset = %#v, error = %v", cleared, runtime.reset, err)
	}
	if _, err := service.RegeneratePairing(ctx, "missing"); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("missing target error = %v", err)
	}
}

func TestSlowTargetStatusSubscriberDoesNotBlockUpdates(t *testing.T) {
	service := NewTargetService([]TargetRegistration{{Info: TargetInfo{ID: "one", Status: "starting"}}}, &targetStoreStub{})
	blocked := make(chan struct{})
	started := make(chan struct{}, 1)
	unsubscribe := service.Subscribe(func(TargetInfo) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-blocked
	})
	service.SetStatus("one", "first")
	<-started
	done := make(chan struct{})
	go func() {
		for index := 0; index < 100; index++ {
			service.SetStatus("one", "running")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber blocked status updates")
	}
	close(blocked)
	unsubscribe()
}
