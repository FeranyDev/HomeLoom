package gormstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

func saveMatterTarget(t testing.TB, ctx context.Context, store *Store, id string) target.Config {
	t.Helper()
	discriminator := uint16(1234)
	item := target.Config{
		ID: id, Type: "matter", Name: id,
		MatterConfig: &target.MatterConfig{
			NetworkInterface: "en0", UDPPort: 5540, Discriminator: &discriminator,
			Passcode: "20202021", VendorID: target.DefaultMatterVendorID,
			ProductID: target.DefaultMatterProductID, ProductName: "HomeLoom Matter Bridge",
			SerialNumber: id, CommissioningWindowSeconds: 900,
		},
	}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatalf("save Matter target: %v", err)
	}
	return item
}

func TestMatterTargetConfigEncryptedRoundTripAndTypeIsImmutable(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	item := saveMatterTarget(t, ctx, store, "matter-config")
	var row targetRow
	if err := store.orm.WithContext(ctx).Where("id = ?", item.ID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.MatterPasscode == item.MatterConfig.Passcode || !strings.HasPrefix(row.MatterPasscode, encryptedPrefix) || row.PIN != "" {
		t.Fatalf("stored Matter secrets = passcode %q pin %q", row.MatterPasscode, row.PIN)
	}
	changed := item
	changed.Type = "apple-hap"
	changed.MatterConfig = nil
	changed.Address, changed.Pin, changed.SetupID = ":51829", "23456789", "LOCK"
	if err := store.SaveTarget(ctx, changed); err == nil {
		t.Fatal("SaveTarget accepted a target type change")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	items, err := store.ListTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var restored *target.Config
	for index := range items {
		if items[index].ID == item.ID {
			restored = &items[index]
		}
	}
	if restored == nil || restored.MatterConfig == nil ||
		restored.MatterConfig.Passcode != item.MatterConfig.Passcode ||
		restored.MatterConfig.Discriminator == nil ||
		*restored.MatterConfig.Discriminator != *item.MatterConfig.Discriminator {
		t.Fatalf("restored Matter target = %#v", restored)
	}
	if restored.Address != "" || restored.Pin != "" || restored.HomeKitConfig != nil {
		t.Fatalf("Matter target leaked HomeKit config = %#v", restored)
	}
}

func TestMatterRuntimeKVEncryptsAndStrictlyIsolatesTargets(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveMatterTarget(t, ctx, store, "matter-one")
	saveMatterTarget(t, ctx, store, "matter-two")
	first := []byte{0, 1, 2, 3, 255}
	second := []byte("other-target-secret")
	if err := store.PutMatterRuntimeValue(ctx, "matter-one", "fabric/1/noc", first); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMatterRuntimeValue(ctx, "matter-two", "fabric/1/noc", second); err != nil {
		t.Fatal(err)
	}
	var rows []matterRuntimeKVRow
	if err := store.orm.WithContext(ctx).Order("target_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Value == string(first) || !strings.HasPrefix(rows[0].Value, encryptedPrefix) {
		t.Fatalf("raw Matter runtime rows = %#v", rows)
	}
	value, found, err := store.GetMatterRuntimeValue(ctx, "matter-one", "fabric/1/noc")
	if err != nil || !found || !bytes.Equal(value, first) {
		t.Fatalf("first namespace value = %v %v %v", value, found, err)
	}
	value, found, err = store.GetMatterRuntimeValue(ctx, "matter-two", "fabric/1/noc")
	if err != nil || !found || !bytes.Equal(value, second) {
		t.Fatalf("second namespace value = %v %v %v", value, found, err)
	}
	value[0] ^= 0xff
	again, _, _ := store.GetMatterRuntimeValue(ctx, "matter-two", "fabric/1/noc")
	if !bytes.Equal(again, second) {
		t.Fatal("runtime value was not returned as an independent buffer")
	}
	if err := store.PutMatterRuntimeValue(ctx, "apple-main", "fabric/1", []byte("x")); err == nil {
		t.Fatal("Matter KV accepted a HomeKit target")
	}
}

func TestMatterRuntimeKVSurvivesRestartAndMissingMasterKeyIsRejected(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	saveMatterTarget(t, ctx, store, "matter-restart")
	want := []byte("persistent-fabric-state")
	if err := store.PutMatterRuntimeValue(ctx, "matter-restart", "fabric-state", want); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := reopened.GetMatterRuntimeValue(ctx, "matter-restart", "fabric-state")
	if err != nil || !found || !bytes.Equal(got, want) {
		t.Fatalf("restarted value = %q %v %v", got, found, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, databaseURL, keyPath); err == nil || !strings.Contains(err.Error(), "master key is missing") {
		t.Fatalf("Open without Matter identity master key = %v", err)
	}
}

func TestMatterRuntimeKVListDeleteAndClearRemainTargetScoped(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveMatterTarget(t, ctx, store, "matter-list-a")
	saveMatterTarget(t, ctx, store, "matter-list-b")
	for _, key := range []string{"counter", "fabric"} {
		if err := store.PutMatterRuntimeValue(ctx, "matter-list-a", key, []byte(key)); err != nil {
			t.Fatal(err)
		}
		if err := store.PutMatterRuntimeValue(ctx, "matter-list-b", key, []byte("b-"+key)); err != nil {
			t.Fatal(err)
		}
	}
	values, err := store.ListMatterRuntimeValues(ctx, "matter-list-a")
	if err != nil || len(values) != 2 || values[0].TargetID != "matter-list-a" {
		t.Fatalf("ListMatterRuntimeValues = %#v, %v", values, err)
	}
	if err := store.DeleteMatterRuntimeValue(ctx, "matter-list-a", "counter"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearMatterRuntimeValues(ctx, "matter-list-a"); err != nil {
		t.Fatal(err)
	}
	other, err := store.ListMatterRuntimeValues(ctx, "matter-list-b")
	if err != nil || len(other) != 2 {
		t.Fatalf("other namespace was changed = %#v, %v", other, err)
	}
}

func TestMatterEndpointConcurrentAllocationIsUniqueAndStable(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveMatterTarget(t, ctx, store, "matter-concurrent")
	const count = 32
	ids := make([]uint16, count)
	errs := make([]error, count)
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			ids[index], errs[index] = store.AllocateMatterEndpoint(ctx, "matter-concurrent", "switch-"+string(rune('a'+index)), device.TypeSwitch)
		}(index)
	}
	wait.Wait()
	seen := make(map[uint16]bool, count)
	for index, endpointID := range ids {
		if errs[index] != nil {
			t.Fatalf("allocation %d failed: %v", index, errs[index])
		}
		if endpointID < 2 || seen[endpointID] {
			t.Fatalf("allocated endpoint IDs = %#v", ids)
		}
		seen[endpointID] = true
	}
	again, err := store.AllocateMatterEndpoint(ctx, "matter-concurrent", "switch-a", device.TypeSwitch)
	if err != nil || again != ids[0] {
		t.Fatalf("stable endpoint = %d, %v; original %d", again, err, ids[0])
	}
	shared := make([]uint16, 16)
	errs = make([]error, len(shared))
	for index := range shared {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			shared[index], errs[index] = store.AllocateMatterEndpoint(ctx, "matter-concurrent", "shared", device.TypeSwitch)
		}(index)
	}
	wait.Wait()
	for index := range shared {
		if errs[index] != nil || shared[index] != shared[0] {
			t.Fatalf("concurrent shared identity = %#v errors=%v", shared, errs)
		}
	}
}

func TestMatterEndpointTombstoneRecoveryNoReuseAndNamespaceIsolation(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	saveMatterTarget(t, ctx, store, "matter-endpoint-a")
	saveMatterTarget(t, ctx, store, "matter-endpoint-b")
	first, err := store.AllocateMatterEndpoint(ctx, "matter-endpoint-a", "virtual-a", device.TypeSwitch)
	if err != nil || first != 2 {
		t.Fatalf("first endpoint = %d, %v", first, err)
	}
	other, err := store.AllocateMatterEndpoint(ctx, "matter-endpoint-b", "virtual-a", device.TypeSwitch)
	if err != nil || other != 2 {
		t.Fatalf("other target first endpoint = %d, %v", other, err)
	}
	if err := store.TombstoneMatterEndpoint(ctx, "matter-endpoint-a", "virtual-a"); err != nil {
		t.Fatal(err)
	}
	second, err := store.AllocateMatterEndpoint(ctx, "matter-endpoint-a", "virtual-b", device.TypeSwitch)
	if err != nil || second != 3 {
		t.Fatalf("endpoint after tombstone = %d, %v", second, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restored, err := store.AllocateMatterEndpoint(ctx, "matter-endpoint-a", "virtual-a", device.TypeSwitch)
	if err != nil || restored != first {
		t.Fatalf("restored endpoint = %d, %v", restored, err)
	}
	identity, found, err := store.MatterEndpointIdentity(ctx, "matter-endpoint-a", "virtual-a")
	if err != nil || !found || identity.Tombstone || identity.EndpointID != first {
		t.Fatalf("restored identity = %#v, %v, %v", identity, found, err)
	}
}

func TestMatterEndpointDeviceTypeChangeRequiresConfirmationAndExhaustionIsExplicit(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveMatterTarget(t, ctx, store, "matter-guard")
	if _, err := store.AllocateMatterEndpoint(ctx, "matter-guard", "virtual", device.TypeSwitch); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AllocateMatterEndpoint(ctx, "matter-guard", "virtual", device.TypeLightbulb); !errors.Is(err, target.ErrMatterDeviceTypeChange) {
		t.Fatalf("device type change = %v", err)
	}
	if err := store.ConfirmMatterEndpointDeviceType(ctx, "matter-guard", "virtual", device.TypeLightbulb, false); !errors.Is(err, target.ErrMatterDeviceTypeChange) {
		t.Fatalf("unconfirmed device type change = %v", err)
	}
	if err := store.ConfirmMatterEndpointDeviceType(ctx, "matter-guard", "virtual", device.TypeLightbulb, true); err != nil {
		t.Fatal(err)
	}
	now := int64(1)
	if err := store.orm.WithContext(ctx).Create(&matterEndpointIdentityRow{
		TargetID: "matter-guard", ConsumerDeviceID: "last", EndpointID: 65534,
		DeviceType: string(device.TypeSwitch), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.AllocateMatterEndpoint(ctx, "matter-guard", "overflow", device.TypeSwitch); !errors.Is(err, target.ErrMatterEndpointIDsExhausted) {
		t.Fatalf("endpoint exhaustion = %v", err)
	}
}

func TestMatterTargetVirtualDeviceUpdatesSynchronizeEndpointTombstones(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := saveMatterTarget(t, ctx, store, "matter-devices")
	item.Devices = []target.VirtualDevice{{
		ID: "virtual", Name: "Switch", Type: device.TypeSwitch,
		SourceDeviceID: "source-switch", Enabled: true,
	}}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AllocateMatterEndpoint(ctx, item.ID, "virtual", device.TypeSwitch); err != nil {
		t.Fatal(err)
	}
	item.Devices = nil
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	identity, found, err := store.MatterEndpointIdentity(ctx, item.ID, "virtual")
	if err != nil || !found || !identity.Tombstone {
		t.Fatalf("identity after removal = %#v, %v, %v", identity, found, err)
	}
	item.Devices = []target.VirtualDevice{{
		ID: "virtual", Name: "Light", Type: device.TypeLightbulb,
		SourceDeviceID: "source-switch", Enabled: true,
	}}
	if err := store.SaveTarget(ctx, item); !errors.Is(err, target.ErrMatterDeviceTypeChange) {
		t.Fatalf("SaveTarget device type change = %v", err)
	}
	item.Devices[0].Type = device.TypeSwitch
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	identity, found, err = store.MatterEndpointIdentity(ctx, item.ID, "virtual")
	if err != nil || !found || identity.Tombstone || identity.EndpointID != 2 {
		t.Fatalf("identity after restoration = %#v, %v, %v", identity, found, err)
	}
}

func TestMatterIdentityBackupContainsEncryptedStateAndRestoresIt(t *testing.T) {
	ctx := context.Background()
	sourceURL, sourceKey := testCredentials(t)
	source, err := Open(ctx, sourceURL, sourceKey)
	if err != nil {
		t.Fatal(err)
	}
	saveMatterTarget(t, ctx, source, "matter-backup")
	if err := source.PutMatterRuntimeValue(ctx, "matter-backup", "fabric/noc", []byte("backup-secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := source.AllocateMatterEndpoint(ctx, "matter-backup", "virtual", device.TypeSwitch); err != nil {
		t.Fatal(err)
	}
	backup := t.TempDir() + "/database.json"
	if err := source.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("backup-secret")) || !bytes.Contains(payload, []byte(encryptedPrefix)) {
		t.Fatalf("backup did not contain only encrypted Matter identity: %s", payload)
	}
	restoreURL, restoreKey := testCredentials(t)
	if _, err := Restore(ctx, backup, restoreURL, restoreKey, true); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, restoreURL, restoreKey)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	value, found, err := restored.GetMatterRuntimeValue(ctx, "matter-backup", "fabric/noc")
	if err != nil || !found || string(value) != "backup-secret" {
		t.Fatalf("restored Matter identity = %q, %v, %v", value, found, err)
	}
	identity, found, err := restored.MatterEndpointIdentity(ctx, "matter-backup", "virtual")
	if err != nil || !found || identity.EndpointID != 2 {
		t.Fatalf("restored endpoint identity = %#v, %v, %v", identity, found, err)
	}
}
