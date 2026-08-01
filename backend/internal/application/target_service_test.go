package application

import (
	"context"
	"errors"
	"strings"
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
	applied         []target.Config
	removed         []string
	reset           []target.Config
	paired          map[string]bool
	matterOperation string
	matterDuration  uint32
	matterFabricID  string
	endpointTarget  string
	endpointID      string
	endpointType    device.Type
	applyErr        error
	removeErr       error
}

type targetDeletionRuntimeStub struct {
	targetRuntimeStub
	deletedTargets []target.Config
}

func (s *targetDeletionRuntimeStub) RemoveTarget(_ context.Context, item target.Config) error {
	s.deletedTargets = append(s.deletedTargets, item)
	return s.removeErr
}

func (s *targetRuntimeStub) Apply(_ context.Context, item target.Config) (TargetRegistration, error) {
	s.applied = append(s.applied, item)
	if s.applyErr != nil {
		return TargetRegistration{}, s.applyErr
	}
	return TargetRegistration{Info: TargetInfo{ID: item.ID, Type: item.Type, Name: item.Name, Enabled: item.Enabled, Status: "running", Address: item.Address, SetupID: item.SetupID, PairingCode: item.Pin, DeviceIDs: item.DeviceIDs, Devices: item.Devices}, QR: []byte("qr")}, nil
}
func (s *targetRuntimeStub) Remove(_ context.Context, id string) error {
	s.removed = append(s.removed, id)
	return s.removeErr
}
func (s *targetRuntimeStub) ResetPairing(_ context.Context, item target.Config) (TargetRegistration, error) {
	s.reset = append(s.reset, item)
	return TargetRegistration{Info: TargetInfo{ID: item.ID, Type: item.Type, Name: item.Name, Enabled: item.Enabled, Status: "running", Address: item.Address, SetupID: item.SetupID, PairingCode: item.Pin, DeviceIDs: item.DeviceIDs, Devices: item.Devices}, QR: []byte("new-qr")}, nil
}
func (s *targetRuntimeStub) IsPaired(item target.Config) bool { return s.paired[item.ID] }
func (s *targetRuntimeStub) matterRegistration(id string, windowOpen bool) TargetRegistration {
	return TargetRegistration{Info: TargetInfo{
		ID: id, Type: "matter", Enabled: true, Status: "running", CommissioningState: "commissioned",
		CommissioningWindowOpen: windowOpen, FabricCount: 1, EndpointCount: 3,
	}}
}
func (s *targetRuntimeStub) OpenCommissioningWindow(_ context.Context, id string, duration uint32) (TargetRegistration, error) {
	s.matterOperation, s.matterDuration = "open", duration
	return s.matterRegistration(id, true), nil
}
func (s *targetRuntimeStub) CloseCommissioningWindow(_ context.Context, id string) (TargetRegistration, error) {
	s.matterOperation = "close"
	return s.matterRegistration(id, false), nil
}
func (s *targetRuntimeStub) RemoveFabric(_ context.Context, id, fabricID string) (TargetRegistration, error) {
	s.matterOperation, s.matterFabricID = "remove-fabric", fabricID
	return s.matterRegistration(id, false), nil
}
func (s *targetRuntimeStub) FactoryResetMatter(_ context.Context, id string) (TargetRegistration, error) {
	s.matterOperation = "factory-reset"
	return s.matterRegistration(id, false), nil
}
func (s *targetRuntimeStub) ConfirmMatterEndpointDeviceType(_ context.Context, targetID, consumerDeviceID string, nextType device.Type) error {
	s.endpointTarget, s.endpointID, s.endpointType = targetID, consumerDeviceID, nextType
	return nil
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
	config := store.saved[0].MatterConfig
	if config == nil || config.Discriminator == nil || *config.Discriminator > 4095 || !validMatterPasscode(config.Passcode) {
		t.Fatalf("Matter commissioning defaults = %#v", config)
	}
	if config.VendorID != target.DefaultMatterVendorID || config.ProductID != target.DefaultMatterProductID ||
		config.ProductName == "" || config.SerialNumber != info.ID ||
		config.CommissioningWindowSeconds != target.DefaultMatterCommissioningWindowSeconds {
		t.Fatalf("Matter product defaults = %#v", config)
	}
}

func TestHomeKitCameraTargetRequiresExactlyOneCamera(t *testing.T) {
	validCamera := target.VirtualDevice{
		ID: "camera-1", Name: "客厅摄像头", Type: device.TypeCamera,
		SourceDeviceID: "camera-1", Enabled: true,
	}
	valid := target.Config{
		ID: "camera-homekit-1", Type: "homekit-camera", Name: "客厅摄像头",
		Address: ":52000", Pin: "12345678", SetupID: "CAM1", StorePath: "data/hap/camera-homekit-1",
		Devices: []target.VirtualDevice{validCamera},
	}
	if err := validateTarget(valid); err != nil {
		t.Fatalf("validateTarget(valid camera) = %v", err)
	}
	for name, mutate := range map[string]func(*target.Config){
		"missing":    func(item *target.Config) { item.Devices = nil },
		"multiple":   func(item *target.Config) { item.Devices = append(item.Devices, validCamera) },
		"wrong type": func(item *target.Config) { item.Devices[0].Type = device.TypeSwitch },
		"disabled":   func(item *target.Config) { item.Devices[0].Enabled = false },
		"auxiliary source": func(item *target.Config) {
			item.Devices[0].AuxiliarySourceDeviceIDs = []string{"camera-2"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			item := valid
			item.Devices = append([]target.VirtualDevice(nil), valid.Devices...)
			mutate(&item)
			if err := validateTarget(item); err == nil {
				t.Fatal("validateTarget() accepted invalid camera target")
			}
		})
	}
}

func TestMatterCameraTargetDefaultsAndRequiresExactlyOneCamera(t *testing.T) {
	store := &targetStoreStub{}
	service := NewTargetService(nil, store)
	camera := target.VirtualDevice{
		ID: "camera-1", Name: "客厅摄像头", Type: device.TypeCamera,
		SourceDeviceID: "camera-1", Enabled: true,
	}
	info, err := service.Save(context.Background(), target.Config{
		ID: "camera-matter-1", Type: "matter-camera", Name: "客厅摄像头",
		Devices: []target.VirtualDevice{camera},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.ConsumerID != "matter-camera" || store.saved[0].MatterConfig == nil ||
		store.saved[0].MatterConfig.ProductName != "HomeLoom Matter Camera" {
		t.Fatalf("Matter Camera defaults = info %#v config %#v", info, store.saved[0])
	}
	for name, mutate := range map[string]func(*target.Config){
		"missing":  func(item *target.Config) { item.Devices = nil },
		"multiple": func(item *target.Config) { item.Devices = append(item.Devices, camera) },
		"wrong type": func(item *target.Config) {
			item.Devices[0].Type = device.TypeSwitch
		},
		"disabled": func(item *target.Config) { item.Devices[0].Enabled = false },
		"auxiliary": func(item *target.Config) {
			item.Devices[0].AuxiliarySourceDeviceIDs = []string{"camera-2"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			item := store.saved[0]
			item.ID = "camera-matter-" + strings.ReplaceAll(name, " ", "-")
			item.Devices = append([]target.VirtualDevice(nil), store.saved[0].Devices...)
			mutate(&item)
			if err := validateTarget(item); err == nil {
				t.Fatal("validateTarget() accepted invalid Matter Camera target")
			}
		})
	}
}

func TestMatterTargetRejectsUnsafeAndCrossProtocolConfiguration(t *testing.T) {
	discriminator := uint16(4096)
	cases := []target.Config{
		{ID: "matter-address", Type: "matter", Name: "Matter", Address: ":51826"},
		{ID: "matter-pin", Type: "matter", Name: "Matter", Pin: "12345678"},
		{ID: "matter-port", Type: "matter", Name: "Matter", MatterConfig: &target.MatterConfig{UDPPort: 80}},
		{ID: "matter-disc", Type: "matter", Name: "Matter", MatterConfig: &target.MatterConfig{Discriminator: &discriminator}},
		{ID: "matter-passcode", Type: "matter", Name: "Matter", MatterConfig: &target.MatterConfig{Passcode: "11111111"}},
		{ID: "matter-window", Type: "matter", Name: "Matter", MatterConfig: &target.MatterConfig{CommissioningWindowSeconds: 901}},
		{ID: "apple-matter", Type: "apple-hap", Name: "HomeKit", MatterConfig: &target.MatterConfig{}},
	}
	for _, item := range cases {
		service := NewTargetService(nil, &targetStoreStub{})
		if _, err := service.Save(context.Background(), item); err == nil {
			t.Fatalf("Save() accepted unsafe target %#v", item)
		}
	}
}

func TestMatterTargetRejectsNonStandardAggregateSensorType(t *testing.T) {
	service := NewTargetService(nil, &targetStoreStub{})
	_, err := service.Save(context.Background(), target.Config{
		ID: "matter-climate", Type: "matter", Name: "Matter",
		Devices: []target.VirtualDevice{{
			ID: "climate", Name: "Climate", Type: device.TypeTemperatureHumiditySensor,
			SourceDeviceID: "source-climate", Enabled: true,
		}},
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Save() error = %v; want ValidationError", err)
	}
	if message := validation.Fields["devices.0.type"]; !strings.Contains(message, "not supported by consumer \"matter\"") {
		t.Fatalf("devices.0.type validation = %q", message)
	}
}

func TestMatterTargetUpdatePreservesGeneratedCommissioningIdentity(t *testing.T) {
	store := &targetStoreStub{}
	service := NewTargetService(nil, store)
	if _, err := service.Save(context.Background(), target.Config{ID: "matter-main", Type: "matter", Name: "Matter"}); err != nil {
		t.Fatal(err)
	}
	created := store.saved[0]
	if _, err := service.Save(context.Background(), target.Config{
		ID: "matter-main", Type: "matter", Name: "Renamed",
		MatterConfig: &target.MatterConfig{
			NetworkInterface: "en0", VendorID: target.DefaultMatterVendorID,
			ProductID: target.DefaultMatterProductID, ProductName: "HomeLoom Matter Bridge",
			SerialNumber: "matter-main", CommissioningWindowSeconds: 900,
		},
	}); err != nil {
		t.Fatal(err)
	}
	updated := store.saved[1]
	if updated.MatterConfig == nil || updated.MatterConfig.Passcode != created.MatterConfig.Passcode ||
		updated.MatterConfig.Discriminator == nil || *updated.MatterConfig.Discriminator != *created.MatterConfig.Discriminator {
		t.Fatalf("commissioning identity changed: created=%#v updated=%#v", created.MatterConfig, updated.MatterConfig)
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

func TestTargetServiceDeleteRemovesProjectionAndPublishesTombstoneWhenRuntimeStopFails(t *testing.T) {
	store := &targetStoreStub{}
	runtime := &targetRuntimeStub{removeErr: errors.New("runtime stop timeout")}
	service := NewTargetService(
		[]TargetRegistration{{Info: TargetInfo{ID: "matter-main", Type: "matter", Name: "Matter", Status: "running"}}},
		store,
		target.Config{ID: "matter-main", Type: "matter", Name: "Matter"},
	)
	service.SetRuntime(runtime)
	events := make(chan TargetInfo, 1)
	unsubscribe := service.Subscribe(func(info TargetInfo) { events <- info })
	defer unsubscribe()

	if err := service.Delete(context.Background(), "matter-main"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if len(service.List()) != 0 {
		t.Fatalf("deleted target remains visible: %#v", service.List())
	}
	if len(store.deleted) != 1 || store.deleted[0] != "matter-main" {
		t.Fatalf("store deletions = %#v", store.deleted)
	}
	if len(runtime.removed) != 1 || runtime.removed[0] != "matter-main" {
		t.Fatalf("runtime removals = %#v", runtime.removed)
	}
	select {
	case event := <-events:
		if event.ID != "matter-main" || !event.Removed {
			t.Fatalf("deletion event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("target deletion tombstone was not published")
	}
}

func TestTargetServiceDeletePassesCameraConfigToDeletionRuntime(t *testing.T) {
	store := &targetStoreStub{}
	runtime := &targetDeletionRuntimeStub{}
	config := target.Config{
		ID: "camera-homekit-1", Type: "homekit-camera", Name: "客厅摄像头", Enabled: true,
		Devices: []target.VirtualDevice{{
			ID: "camera-1", SourceDeviceID: "camera-1", Type: device.TypeCamera, Enabled: true,
		}},
	}
	service := NewTargetService(
		[]TargetRegistration{{Info: TargetInfo{ID: config.ID, Type: config.Type, Status: "running"}}},
		store,
		config,
	)
	service.SetRuntime(runtime)
	if err := service.Delete(context.Background(), config.ID); err != nil {
		t.Fatal(err)
	}
	if len(runtime.deletedTargets) != 1 || runtime.deletedTargets[0].Devices[0].SourceDeviceID != "camera-1" {
		t.Fatalf("deletion runtime configs = %#v", runtime.deletedTargets)
	}
	if len(runtime.removed) != 0 {
		t.Fatalf("legacy runtime removal was also called: %#v", runtime.removed)
	}
}

func TestTargetServiceDeleteKeepsCameraWhenPairingCleanupFails(t *testing.T) {
	store := &targetStoreStub{}
	runtime := &targetDeletionRuntimeStub{}
	runtime.removeErr = errors.New("pairing store is read-only")
	config := target.Config{
		ID: "camera-homekit-1", Type: "homekit-camera", Name: "客厅摄像头", Enabled: true,
		Devices: []target.VirtualDevice{{
			ID: "camera-1", SourceDeviceID: "camera-1", Type: device.TypeCamera, Enabled: true,
		}},
	}
	service := NewTargetService(
		[]TargetRegistration{{Info: TargetInfo{ID: config.ID, Type: config.Type, Name: config.Name, Status: "running"}}},
		store,
		config,
	)
	service.SetRuntime(runtime)
	events := make(chan TargetInfo, 1)
	unsubscribe := service.Subscribe(func(info TargetInfo) { events <- info })
	defer unsubscribe()

	err := service.Delete(context.Background(), config.ID)
	if err == nil || !strings.Contains(err.Error(), "pairing identity") {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("persistent target was deleted despite cleanup failure: %#v", store.deleted)
	}
	if items := service.List(); len(items) != 1 || items[0].ID != config.ID || items[0].Removed {
		t.Fatalf("failed deletion changed target projection: %#v", items)
	}
	select {
	case event := <-events:
		t.Fatalf("failed deletion published a tombstone: %#v", event)
	default:
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
	unsupported := map[device.Type]bool{
		// Camera accessories are owned by the isolated Media Worker rather
		// than the ordinary HomeKit bridge target.
		device.TypeCamera:         true,
		device.TypePressureSensor: true, device.TypeNoiseSensor: true,
		device.TypeWaterLevelSensor: true, device.TypeSoilMoistureSensor: true,
		device.TypePump: true, device.TypeWaterHeater: true, device.TypePowerMeter: true,
		device.TypeEVCharger: true, device.TypeRobotVacuum: true,
	}
	for _, model := range device.ModelContracts() {
		known, supported := mapping.ConsumerModelSupport("homekit", model.DeviceType)
		if !known {
			t.Fatalf("HomeKit consumer registry is missing")
		}
		if !supported {
			if !unsupported[model.DeviceType] {
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
	if len(store.saved) != 1 || len(store.saved[0].Devices) != len(device.ModelContracts())-len(unsupported) {
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
	updated.HomeKitConfig = &target.HomeKitConfig{
		Address: config.Address, Pin: "22223333", SetupID: "PTR1", StorePath: config.StorePath,
	}
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

func TestMatterRuntimeControlsUpdateRegistration(t *testing.T) {
	discriminator := uint16(1234)
	config := target.Config{
		ID: "matter-main", Type: "matter", Name: "Matter", Enabled: true,
		MatterConfig: &target.MatterConfig{
			Discriminator: &discriminator, Passcode: "20202021", VendorID: 0xfff1, ProductID: 0x8000,
			ProductName: "HomeLoom", SerialNumber: "matter-main", CommissioningWindowSeconds: 900,
		},
	}
	runtime := &targetRuntimeStub{}
	service := NewTargetService([]TargetRegistration{{Info: TargetInfo{ID: config.ID, Type: "matter"}}}, &targetStoreStub{}, config)
	service.SetRuntime(runtime)
	info, err := service.OpenMatterCommissioningWindow(context.Background(), config.ID, 300)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.matterOperation != "open" || runtime.matterDuration != 300 || !info.CommissioningWindowOpen {
		t.Fatalf("open operation = %q %d, info=%#v", runtime.matterOperation, runtime.matterDuration, info)
	}
	if _, err := service.RemoveMatterFabric(context.Background(), config.ID, "fabric-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.matterOperation != "remove-fabric" || runtime.matterFabricID != "fabric-1" {
		t.Fatalf("fabric operation = %q %q", runtime.matterOperation, runtime.matterFabricID)
	}
	if _, err := service.FactoryResetMatter(context.Background(), config.ID); err != nil || runtime.matterOperation != "factory-reset" {
		t.Fatalf("factory reset = %q, %v", runtime.matterOperation, err)
	}
}

func TestMatterRuntimeControlsRejectWrongTargetAndUnsafeDuration(t *testing.T) {
	homekit := target.Config{ID: "apple", Type: "apple-hap"}
	matter := target.Config{ID: "matter", Type: "matter", MatterConfig: &target.MatterConfig{CommissioningWindowSeconds: 900}}
	runtime := &targetRuntimeStub{}
	service := NewTargetService(nil, &targetStoreStub{}, homekit, matter)
	service.SetRuntime(runtime)
	if _, err := service.OpenMatterCommissioningWindow(context.Background(), "missing", 300); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("missing target error = %v", err)
	}
	if _, err := service.CloseMatterCommissioningWindow(context.Background(), homekit.ID); err == nil {
		t.Fatal("HomeKit target accepted Matter operation")
	}
	if _, err := service.OpenMatterCommissioningWindow(context.Background(), matter.ID, 179); err == nil {
		t.Fatal("unsafe commissioning duration was accepted")
	}
}

func TestConfirmMatterEndpointDeviceTypePersistsIdentityAndReappliesMultipleChanges(t *testing.T) {
	discriminator := uint16(1234)
	config := target.Config{
		ID: "matter-main", Type: "matter", Name: "Matter", Enabled: true,
		MatterConfig: &target.MatterConfig{
			Discriminator: &discriminator, Passcode: "20202021", VendorID: 0xfff1, ProductID: 0x8000,
			ProductName: "HomeLoom", SerialNumber: "matter-main", CommissioningWindowSeconds: 900,
		},
		Devices: []target.VirtualDevice{{
			ID: "living-room", Name: "Living room", Type: device.TypeSwitch,
			SourceDeviceID: "source-switch", Enabled: true,
		}, {
			ID: "desk-outlet", Name: "Desk outlet", Type: device.TypeOutlet,
			SourceDeviceID: "source-outlet", Enabled: true,
		}},
	}
	store, runtime := &targetStoreStub{}, &targetRuntimeStub{}
	service := NewTargetService([]TargetRegistration{{Info: TargetInfo{ID: config.ID, Type: "matter"}}}, store, config)
	service.SetRuntime(runtime)

	info, err := service.ConfirmMatterEndpointDeviceType(context.Background(), config.ID, "living-room", device.TypeLightbulb)
	if err != nil {
		t.Fatal(err)
	}
	info, err = service.ConfirmMatterEndpointDeviceType(context.Background(), config.ID, "desk-outlet", device.TypeContactSensor)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.endpointTarget != config.ID || runtime.endpointID != "desk-outlet" || runtime.endpointType != device.TypeContactSensor {
		t.Fatalf("endpoint confirmation = %q %q %q", runtime.endpointTarget, runtime.endpointID, runtime.endpointType)
	}
	if len(store.saved) != 2 || store.saved[0].Devices[0].Type != device.TypeLightbulb ||
		store.saved[1].Devices[0].Type != device.TypeLightbulb || store.saved[1].Devices[1].Type != device.TypeContactSensor {
		t.Fatalf("saved target = %#v", store.saved)
	}
	if len(runtime.applied) != 2 || runtime.applied[1].Devices[0].Type != device.TypeLightbulb ||
		runtime.applied[1].Devices[1].Type != device.TypeContactSensor ||
		info.Devices[0].Type != device.TypeLightbulb || info.Devices[1].Type != device.TypeContactSensor {
		t.Fatalf("reapplied target = %#v, info=%#v", runtime.applied, info)
	}
}

func TestConfirmMatterEndpointDeviceTypeRejectsUnownedEndpoint(t *testing.T) {
	config := target.Config{ID: "matter-main", Type: "matter", MatterConfig: &target.MatterConfig{}}
	service := NewTargetService(nil, &targetStoreStub{}, config)
	service.SetRuntime(&targetRuntimeStub{})
	if _, err := service.ConfirmMatterEndpointDeviceType(context.Background(), config.ID, "missing", device.TypeSwitch); err == nil {
		t.Fatal("confirmation accepted an endpoint outside the target")
	}
}

func TestConfirmMatterEndpointDeviceTypeKeepsPersistedConfigWhenRuntimeApplyFails(t *testing.T) {
	discriminator := uint16(1234)
	config := target.Config{
		ID: "matter-main", Type: "matter", Name: "Matter", MatterConfig: &target.MatterConfig{
			Discriminator: &discriminator, Passcode: "20202021", VendorID: 0xfff1, ProductID: 0x8000,
			ProductName: "HomeLoom", SerialNumber: "matter-main", CommissioningWindowSeconds: 900,
		},
		Devices: []target.VirtualDevice{{ID: "lamp", Name: "Lamp", Type: device.TypeSwitch, SourceDeviceID: "source", Enabled: true}},
	}
	store := &targetStoreStub{}
	runtime := &targetRuntimeStub{applyErr: errors.New("runtime unavailable")}
	service := NewTargetService([]TargetRegistration{{Info: TargetInfo{ID: config.ID, Type: "matter"}}}, store, config)
	service.SetRuntime(runtime)
	info, err := service.ConfirmMatterEndpointDeviceType(context.Background(), config.ID, "lamp", device.TypeLightbulb)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "error" || !strings.Contains(info.Error, "runtime unavailable") {
		t.Fatalf("error info = %#v", info)
	}
	if service.configs[config.ID].Devices[0].Type != device.TypeLightbulb || store.saved[0].Devices[0].Type != device.TypeLightbulb {
		t.Fatalf("service and store diverged: config=%#v saved=%#v", service.configs[config.ID], store.saved)
	}
}
