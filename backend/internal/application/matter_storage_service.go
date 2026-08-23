package application

import (
	"context"
	"errors"
	"strconv"
	"time"

	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

// MatterStorageStore is the persistence boundary used by the Matter runtime
// RPC handlers. Every operation carries targetID explicitly so an RPC session
// can be bound to exactly one namespace.
type MatterStorageStore interface {
	PutMatterRuntimeValue(context.Context, string, string, []byte) error
	GetMatterRuntimeValue(context.Context, string, string) ([]byte, bool, error)
	ListMatterRuntimeValues(context.Context, string) ([]target.MatterRuntimeValue, error)
	DeleteMatterRuntimeValue(context.Context, string, string) error
	ClearMatterRuntimeValues(context.Context, string) error
	// AllocateMatterEndpoint reports whether it created or restored an identity.
	// Reusing an active endpoint is intentionally not a state change.
	AllocateMatterEndpoint(context.Context, string, string, device.Type) (uint16, bool, error)
	TombstoneMatterEndpoint(context.Context, string, string) error
	ConfirmMatterEndpointDeviceType(context.Context, string, string, device.Type, bool) error
	MatterEndpointIdentity(context.Context, string, string) (target.MatterEndpointIdentity, bool, error)
	ListMatterEndpointIdentities(context.Context, string) ([]target.MatterEndpointIdentity, error)
}

type MatterStorageService struct {
	store MatterStorageStore
	audit interface {
		AppendAuditEvent(context.Context, domainaudit.Event) (domainaudit.Event, error)
	}
}

func NewMatterStorageService(store MatterStorageStore) *MatterStorageService {
	service := &MatterStorageService{store: store}
	service.audit, _ = store.(interface {
		AppendAuditEvent(context.Context, domainaudit.Event) (domainaudit.Event, error)
	})
	return service
}

func (s *MatterStorageService) available() error {
	if s == nil || s.store == nil {
		return errors.New("Matter storage is unavailable")
	}
	return nil
}

func (s *MatterStorageService) Put(ctx context.Context, targetID, key string, value []byte) error {
	if err := s.available(); err != nil {
		return err
	}
	if err := s.store.PutMatterRuntimeValue(ctx, targetID, key, append([]byte(nil), value...)); err != nil {
		return err
	}
	// Matter runtimes checkpoint identity state frequently. These opaque storage
	// writes are not administrative lifecycle events and must not fill the
	// operator audit log.
	return nil
}

func (s *MatterStorageService) Get(ctx context.Context, targetID, key string) ([]byte, bool, error) {
	if err := s.available(); err != nil {
		return nil, false, err
	}
	value, found, err := s.store.GetMatterRuntimeValue(ctx, targetID, key)
	return append([]byte(nil), value...), found, err
}

func (s *MatterStorageService) List(ctx context.Context, targetID string) ([]target.MatterRuntimeValue, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	values, err := s.store.ListMatterRuntimeValues(ctx, targetID)
	if err != nil {
		return nil, err
	}
	for index := range values {
		values[index].Value = append([]byte(nil), values[index].Value...)
	}
	return values, nil
}

func (s *MatterStorageService) Delete(ctx context.Context, targetID, key string) error {
	if err := s.available(); err != nil {
		return err
	}
	if err := s.store.DeleteMatterRuntimeValue(ctx, targetID, key); err != nil {
		return err
	}
	return s.record(ctx, "matter.identity.delete", targetID)
}

func (s *MatterStorageService) Clear(ctx context.Context, targetID string) error {
	if err := s.available(); err != nil {
		return err
	}
	if err := s.store.ClearMatterRuntimeValues(ctx, targetID); err != nil {
		return err
	}
	return s.record(ctx, "matter.identity.clear", targetID)
}

func (s *MatterStorageService) AllocateEndpoint(ctx context.Context, targetID, consumerDeviceID string, deviceType device.Type) (uint16, error) {
	if err := s.available(); err != nil {
		return 0, err
	}
	id, changed, err := s.store.AllocateMatterEndpoint(ctx, targetID, consumerDeviceID, deviceType)
	if err == nil && changed {
		err = s.record(ctx, "matter.endpoint.allocate", targetID,
			domainaudit.Detail{Label: "Matter 端点", Value: strconv.FormatUint(uint64(id), 10)},
			domainaudit.Detail{Label: "设备", Value: consumerDeviceID},
		)
	}
	return id, err
}

func (s *MatterStorageService) TombstoneEndpoint(ctx context.Context, targetID, consumerDeviceID string) error {
	if err := s.available(); err != nil {
		return err
	}
	if err := s.store.TombstoneMatterEndpoint(ctx, targetID, consumerDeviceID); err != nil {
		return err
	}
	return s.record(ctx, "matter.endpoint.tombstone", targetID)
}

func (s *MatterStorageService) ConfirmEndpointDeviceType(ctx context.Context, targetID, consumerDeviceID string, deviceType device.Type, confirmed bool) error {
	if err := s.available(); err != nil {
		return err
	}
	if err := s.store.ConfirmMatterEndpointDeviceType(ctx, targetID, consumerDeviceID, deviceType, confirmed); err != nil {
		return err
	}
	return s.record(ctx, "matter.endpoint.device-type-confirm", targetID)
}

func (s *MatterStorageService) Endpoint(ctx context.Context, targetID, consumerDeviceID string) (target.MatterEndpointIdentity, bool, error) {
	if err := s.available(); err != nil {
		return target.MatterEndpointIdentity{}, false, err
	}
	return s.store.MatterEndpointIdentity(ctx, targetID, consumerDeviceID)
}

func (s *MatterStorageService) Endpoints(ctx context.Context, targetID string) ([]target.MatterEndpointIdentity, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	return s.store.ListMatterEndpointIdentities(ctx, targetID)
}

func (s *MatterStorageService) record(ctx context.Context, action, targetID string, details ...domainaudit.Detail) error {
	if s.audit == nil {
		return nil
	}
	_, err := s.audit.AppendAuditEvent(ctx, domainaudit.Event{
		CorrelationID: CorrelationID(ctx), Actor: "matter-runtime", Action: action,
		ResourceType: "matter-identity", ResourceID: targetID,
		Method: "IPC", Route: "matter-runtime/storage", Status: 200,
		Outcome: domainaudit.OutcomeSucceeded, Details: details, CreatedAt: time.Now().UTC(),
	})
	return err
}
