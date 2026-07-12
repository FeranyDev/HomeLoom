package application

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

type targetStoreStub struct {
	saved   []target.Config
	deleted []string
}

func (s *targetStoreStub) SaveTarget(_ context.Context, item target.Config) error {
	s.saved = append(s.saved, item)
	return nil
}
func (s *targetStoreStub) DeleteTarget(_ context.Context, id string) error {
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
	service.SetStatus("one", "running")
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
