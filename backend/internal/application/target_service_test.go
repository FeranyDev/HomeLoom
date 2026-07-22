package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/mapping"
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
	paired  map[string]bool
}

func (s *targetRuntimeStub) Apply(_ context.Context, item target.Config) (TargetRegistration, error) {
	s.applied = append(s.applied, item)
	return TargetRegistration{Info: TargetInfo{ID: item.ID, Type: item.Type, Name: item.Name, Enabled: item.Enabled, Status: "running", Address: item.Address, SetupID: item.SetupID, PairingCode: item.Pin, DeviceIDs: item.DeviceIDs, Devices: item.Devices}, QR: []byte("qr")}, nil
}
func (s *targetRuntimeStub) Remove(context.Context, string) error { return nil }
func (s *targetRuntimeStub) ResetPairing(_ context.Context, item target.Config) (TargetRegistration, error) {
	s.reset = append(s.reset, item)
	return TargetRegistration{Info: TargetInfo{ID: item.ID, Type: item.Type, Name: item.Name, Enabled: item.Enabled, Status: "running", Address: item.Address, SetupID: item.SetupID, PairingCode: item.Pin, DeviceIDs: item.DeviceIDs, Devices: item.Devices}, QR: []byte("new-qr")}, nil
}
func (s *targetRuntimeStub) IsPaired(item target.Config) bool { return s.paired[item.ID] }

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
	if info.ID == "" || info.Name == "" || info.ConsumerID != "homekit" || info.Address == "" || info.SetupID == "" || info.PairingCode == "" {
		t.Fatalf("generated target info = %#v", info)
	}
	if len(store.saved) != 1 || store.saved[0].StorePath != "data/hap/"+info.ID {
		t.Fatalf("stored target = %#v", store.saved)
	}
}

func TestTargetDefaultsFollowSelectedAdapter(t *testing.T) {
	store := &targetStoreStub{}
	service := NewTargetService(nil, store)
	info, err := service.Save(context.Background(), target.Config{Type: "matter", Enabled: false})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if len(info.ID) < len("matter-") || info.ID[:len("matter-")] != "matter-" {
		t.Fatalf("Matter target ID = %q", info.ID)
	}
	if info.Name != "HomeLoom Matter Bridge" || info.ConsumerID != "matter" || info.Address != "" || info.SetupID != "" || info.PairingCode != "" {
		t.Fatalf("Matter target defaults leaked HomeKit fields: %#v", info)
	}
	if len(store.saved) != 1 || store.saved[0].StorePath != "" || store.saved[0].Pin != "" {
		t.Fatalf("stored Matter target = %#v", store.saved)
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
		{ID: "id", Type: "apple-hap", Name: "name", Pin: "12345678", Address: ":1", SetupID: "ABCD", StorePath: "data/hap/id", Devices: []target.VirtualDevice{{ID: "vacuum", Name: "Vacuum", Type: device.TypeRobotVacuum, SourceDeviceID: "vacuum-source"}}},
	}
	for _, item := range cases {
		if _, err := service.Save(context.Background(), item); err == nil {
			t.Fatalf("Save() accepted %#v", item)
		}
	}
}

func TestTargetSaveAcceptsHomeKitAirConditioner(t *testing.T) {
	store := &targetStoreStub{}
	service := NewTargetService(nil, store)
	config := target.Config{
		ID: "apple-main", Type: "apple-hap", Name: "Main", Pin: "12345678", Address: ":51826", SetupID: "HLM1", StorePath: "data/hap/apple-main",
		Devices: []target.VirtualDevice{{ID: "living-room-ac", Name: "客厅空调", Type: device.TypeAirConditioner, SourceDeviceID: "xiaomi-ac", Enabled: true}},
	}
	if _, err := service.Save(context.Background(), config); err != nil {
		t.Fatalf("Save() rejected HomeKit air conditioner: %v", err)
	}
	if len(store.saved) != 1 || store.saved[0].Devices[0].Type != device.TypeAirConditioner {
		t.Fatalf("saved target = %#v", store.saved)
	}
}

func TestTargetSaveAcceptsIndependentConsumerTypeAndAuxiliarySources(t *testing.T) {
	store := &targetStoreStub{}
	service := NewTargetService(nil, store)
	config := target.Config{
		ID: "apple-aggregate", Type: "apple-hap", Name: "Aggregate", Pin: "12345678", Address: ":51826", SetupID: "AGG1", StorePath: "data/hap/apple-aggregate",
		Devices: []target.VirtualDevice{{ID: "homekit-outlet", Name: "组合插座", Type: device.TypeOutlet, SourceDeviceID: "source-switch", AuxiliarySourceDeviceIDs: []string{"source-power-meter"}, Enabled: true}},
	}
	info, err := service.Save(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.DeviceIDs) != 2 || len(store.saved) != 1 || len(store.saved[0].Devices[0].AuxiliarySourceDeviceIDs) != 1 {
		t.Fatalf("saved aggregate target = %#v", store.saved)
	}
	config.Devices[0].AuxiliarySourceDeviceIDs = []string{"source-switch"}
	if _, err := service.Save(context.Background(), config); err == nil {
		t.Fatal("Save() accepted a primary source duplicated as an auxiliary source")
	}
}

func TestTargetSaveAcceptsEveryHomeKitConsumerModel(t *testing.T) {
	store := &targetStoreStub{}
	service := NewTargetService(nil, store)
	var devices []target.VirtualDevice
	for _, model := range device.ModelContracts() {
		known, supported := mapping.ConsumerModelSupport("homekit", model.DeviceType)
		if !known {
			t.Fatalf("HomeKit consumer registry is missing")
		}
		if !supported {
			if model.DeviceType != device.TypeRobotVacuum {
				t.Errorf("unexpected unsupported HomeKit model %q", model.DeviceType)
			}
			continue
		}
		id := "homekit-" + string(model.DeviceType)
		devices = append(devices, target.VirtualDevice{ID: id, Name: id, Type: model.DeviceType, SourceDeviceID: "source-" + string(model.DeviceType), Enabled: true})
	}
	config := target.Config{ID: "apple-all", Type: "apple-hap", Name: "All", Pin: "12345678", Address: ":51826", SetupID: "ALL1", StorePath: "data/hap/apple-all", Devices: devices}
	if _, err := service.Save(context.Background(), config); err != nil {
		t.Fatalf("Save() rejected a HomeKit-supported model: %v", err)
	}
	if len(store.saved) != 1 || len(store.saved[0].Devices) != len(device.ModelContracts())-1 {
		t.Fatalf("saved HomeKit devices = %d", len(store.saved[0].Devices))
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

func TestPairedTargetHidesAndLocksOneTimeSetupParameters(t *testing.T) {
	ctx := context.Background()
	config := target.Config{ID: "apple-main", Type: "apple-hap", Name: "Main", Enabled: true, Address: ":51826", Pin: "12345678", SetupID: "LOCK", StorePath: "data/hap/apple-main"}
	store := &targetStoreStub{}
	runtime := &targetRuntimeStub{paired: map[string]bool{config.ID: true}}
	service := NewTargetService([]TargetRegistration{{Info: TargetInfo{
		ID: config.ID, Type: config.Type, Name: config.Name, Enabled: true, Status: "running",
		Address: config.Address, SetupID: config.SetupID, PairingCode: "123-45-678", SetupURI: "X-HM://secret",
	}, QR: []byte("qr")}}, store, config)
	service.SetRuntime(runtime)

	items := service.List()
	if len(items) != 1 || !items[0].Paired || items[0].SetupID != "" || items[0].PairingCode != "" || items[0].SetupURI != "" {
		t.Fatalf("paired target info = %#v", items)
	}
	if _, err := service.QR(config.ID); err == nil {
		t.Fatal("paired target exposed its setup QR")
	}
	if _, err := service.RegeneratePairing(ctx, config.ID); err == nil {
		t.Fatal("paired target regenerated one-time setup parameters")
	}

	updated := config
	updated.Name = "Renamed"
	updated.Pin = "87654321"
	updated.SetupID = "NEW1"
	savedInfo, err := service.Save(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if !savedInfo.Paired || savedInfo.SetupID != "" || savedInfo.PairingCode != "" {
		t.Fatalf("paired Save() response = %#v", savedInfo)
	}
	if len(store.saved) != 1 || store.saved[0].Pin != config.Pin || store.saved[0].SetupID != config.SetupID || store.saved[0].Name != updated.Name {
		t.Fatalf("saved paired target = %#v", store.saved)
	}
	updated.Type = "matter"
	if _, err := service.Save(ctx, updated); err == nil {
		t.Fatal("paired target changed its adapter type")
	}
}

func TestTargetRefreshRebuildsOnlyEnabledConsumerGraphs(t *testing.T) {
	enabled := target.Config{ID: "apple-main", Type: "apple-hap", Name: "Main", Enabled: true, Address: ":51826", Pin: "12345678", SetupID: "MAIN", StorePath: "data/hap/apple-main"}
	disabled := target.Config{ID: "apple-off", Type: "apple-hap", Name: "Off", Enabled: false, Address: ":51827", Pin: "12345678", SetupID: "OFF1", StorePath: "data/hap/apple-off"}
	runtime := &targetRuntimeStub{}
	service := NewTargetService(nil, &targetStoreStub{}, enabled, disabled)
	service.SetRuntime(runtime)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.applied) != 1 || runtime.applied[0].ID != enabled.ID {
		t.Fatalf("refreshed targets = %#v", runtime.applied)
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
