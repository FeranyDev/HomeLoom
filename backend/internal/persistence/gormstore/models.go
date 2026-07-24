package gormstore

import (
	"database/sql/driver"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Persistence models define the schema shared by PostgreSQL and SQLite. Domain
// objects stay independent from GORM and are converted at the repository edge.

type jsonDocument string

func (jsonDocument) GormDataType() string { return "json" }

func (jsonDocument) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "postgres" {
		return "JSONB"
	}
	return "TEXT"
}

func (j jsonDocument) Value() (driver.Value, error) { return string(j), nil }

func (j *jsonDocument) Scan(value any) error {
	switch current := value.(type) {
	case string:
		*j = jsonDocument(current)
	case []byte:
		*j = jsonDocument(current)
	case nil:
		*j = ""
	default:
		return fmt.Errorf("scan JSON document from %T", value)
	}
	return nil
}

type providerRow struct {
	ID         string       `gorm:"column:id;primaryKey"`
	Type       string       `gorm:"column:type;not null"`
	Name       string       `gorm:"column:name;not null"`
	Enabled    bool         `gorm:"column:enabled;not null;default:false"`
	ConfigJSON jsonDocument `gorm:"column:config_json;not null;default:'{}'"`
	CreatedAt  int64        `gorm:"column:created_at;not null"`
	UpdatedAt  int64        `gorm:"column:updated_at;not null"`
}

func (providerRow) TableName() string { return "providers" }

type targetRow struct {
	ID                               string `gorm:"column:id;primaryKey"`
	Type                             string `gorm:"column:type;not null"`
	Name                             string `gorm:"column:name;not null"`
	Enabled                          bool   `gorm:"column:enabled;not null;default:false"`
	Address                          string `gorm:"column:address;not null;default:'';uniqueIndex:targets_enabled_address,where:enabled = true AND address <> ''"`
	PIN                              string `gorm:"column:pin;not null;default:''"`
	SetupID                          string `gorm:"column:setup_id;not null;default:'';uniqueIndex:targets_enabled_setup_id,where:enabled = true AND setup_id <> ''"`
	StorePath                        string `gorm:"column:store_path;not null;default:'';uniqueIndex:targets_enabled_store_path,where:enabled = true AND store_path <> ''"`
	MatterNetworkInterface           string `gorm:"column:matter_network_interface;not null;default:''"`
	MatterUDPPort                    uint32 `gorm:"column:matter_udp_port;not null;default:0"`
	MatterDiscriminator              uint32 `gorm:"column:matter_discriminator;not null;default:0"`
	MatterPasscode                   string `gorm:"column:matter_passcode;not null;default:''"`
	MatterVendorID                   uint32 `gorm:"column:matter_vendor_id;not null;default:0"`
	MatterProductID                  uint32 `gorm:"column:matter_product_id;not null;default:0"`
	MatterProductName                string `gorm:"column:matter_product_name;not null;default:''"`
	MatterSerialNumber               string `gorm:"column:matter_serial_number;not null;default:''"`
	MatterCommissioningWindowSeconds uint32 `gorm:"column:matter_commissioning_window_seconds;not null;default:0"`
	CreatedAt                        int64  `gorm:"column:created_at;not null"`
	UpdatedAt                        int64  `gorm:"column:updated_at;not null"`
}

func (targetRow) TableName() string { return "targets" }

type targetVirtualDeviceRow struct {
	TargetID                     string       `gorm:"column:target_id;primaryKey"`
	ID                           string       `gorm:"column:id;primaryKey"`
	Name                         string       `gorm:"column:name;not null"`
	Type                         string       `gorm:"column:type;not null;default:''"`
	SourceDeviceID               string       `gorm:"column:source_device_id;not null"`
	AuxiliarySourceDeviceIDsJSON jsonDocument `gorm:"column:auxiliary_source_device_ids;not null;default:'[]'"`
	Enabled                      bool         `gorm:"column:enabled;not null"`
	CreatedAt                    int64        `gorm:"column:created_at;not null"`
	UpdatedAt                    int64        `gorm:"column:updated_at;not null"`
	Target                       targetRow    `gorm:"foreignKey:TargetID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (targetVirtualDeviceRow) TableName() string { return "target_virtual_devices" }

type matterRuntimeKVRow struct {
	TargetID  string    `gorm:"column:target_id;primaryKey"`
	Key       string    `gorm:"column:key;primaryKey"`
	Value     string    `gorm:"column:value;not null"`
	Sensitive bool      `gorm:"column:sensitive;not null;default:true"`
	UpdatedAt int64     `gorm:"column:updated_at;not null"`
	Target    targetRow `gorm:"foreignKey:TargetID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (matterRuntimeKVRow) TableName() string { return "matter_runtime_kv" }

type matterEndpointIdentityRow struct {
	TargetID         string    `gorm:"column:target_id;primaryKey;uniqueIndex:matter_endpoint_target_endpoint,priority:1"`
	ConsumerDeviceID string    `gorm:"column:consumer_device_id;primaryKey"`
	EndpointID       uint32    `gorm:"column:endpoint_id;not null;uniqueIndex:matter_endpoint_target_endpoint,priority:2;check:matter_endpoint_id_range,endpoint_id >= 2 AND endpoint_id <= 65534"`
	DeviceType       string    `gorm:"column:device_type;not null"`
	Tombstone        bool      `gorm:"column:tombstone;not null;default:false"`
	CreatedAt        int64     `gorm:"column:created_at;not null"`
	UpdatedAt        int64     `gorm:"column:updated_at;not null"`
	Target           targetRow `gorm:"foreignKey:TargetID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (matterEndpointIdentityRow) TableName() string { return "matter_endpoint_identities" }

type homeKitAccessoryIDRow struct {
	TargetID  string    `gorm:"column:target_id;primaryKey;uniqueIndex:homekit_accessory_target_aid,priority:1"`
	DeviceID  string    `gorm:"column:device_id;primaryKey"`
	AID       uint64    `gorm:"column:aid;not null;uniqueIndex:homekit_accessory_target_aid,priority:2;check:homekit_accessory_aid_min,aid >= 2"`
	CreatedAt int64     `gorm:"column:created_at;not null"`
	Target    targetRow `gorm:"foreignKey:TargetID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (homeKitAccessoryIDRow) TableName() string { return "homekit_accessory_ids" }

type homeKitIIDRow struct {
	TargetID    string    `gorm:"column:target_id;primaryKey;uniqueIndex:homekit_target_device_iid,priority:1"`
	DeviceID    string    `gorm:"column:device_id;primaryKey;uniqueIndex:homekit_target_device_iid,priority:2"`
	ResourceKey string    `gorm:"column:resource_key;primaryKey"`
	IID         uint64    `gorm:"column:iid;not null;uniqueIndex:homekit_target_device_iid,priority:3;check:homekit_iid_min,iid >= 1"`
	CreatedAt   int64     `gorm:"column:created_at;not null"`
	Target      targetRow `gorm:"foreignKey:TargetID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (homeKitIIDRow) TableName() string { return "homekit_iids" }

type systemSettingRow struct {
	Key       string `gorm:"column:key;primaryKey"`
	Value     string `gorm:"column:value;not null"`
	UpdatedAt int64  `gorm:"column:updated_at;not null"`
}

func (systemSettingRow) TableName() string { return "system_settings" }

type devicePreferenceRow struct {
	DeviceID  string `gorm:"column:device_id;primaryKey"`
	Disabled  bool   `gorm:"column:disabled;not null;default:false"`
	UpdatedAt int64  `gorm:"column:updated_at;not null"`
}

func (devicePreferenceRow) TableName() string { return "device_preferences" }

type auditEventRow struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement"`
	CorrelationID string `gorm:"column:correlation_id;not null;index:audit_events_correlation_id_idx"`
	Actor         string `gorm:"column:actor;not null"`
	Action        string `gorm:"column:action;not null"`
	ResourceType  string `gorm:"column:resource_type;not null"`
	ResourceID    string `gorm:"column:resource_id;not null;default:''"`
	Method        string `gorm:"column:method;not null"`
	Route         string `gorm:"column:route;not null"`
	Status        int    `gorm:"column:status;not null"`
	Outcome       string `gorm:"column:outcome;not null"`
	CreatedAt     int64  `gorm:"column:created_at;not null;index:audit_events_created_at_idx,sort:desc"`
}

func (auditEventRow) TableName() string { return "audit_events" }

type mappingProfileRow struct {
	ID           string       `gorm:"column:id;primaryKey;index:mapping_profiles_kind_id_idx,priority:2"`
	Kind         string       `gorm:"column:kind;not null;index:mapping_profiles_kind_id_idx,priority:1;check:mapping_profiles_kind_check,kind IN ('provider','capability','target')"`
	Version      int          `gorm:"column:version;not null;check:mapping_profiles_version_check,version > 0"`
	DocumentJSON jsonDocument `gorm:"column:document_json;not null"`
	CreatedAt    int64        `gorm:"column:created_at;not null"`
	UpdatedAt    int64        `gorm:"column:updated_at;not null"`
}

func (mappingProfileRow) TableName() string { return "mapping_profiles" }

type mappingBindingRow struct {
	ID                 string `gorm:"column:id;primaryKey"`
	Stage              string `gorm:"column:stage;not null;check:mapping_bindings_stage_check,stage IN ('provider','consumer');index:mapping_provider_source_idx,where:stage = 'provider',priority:1;uniqueIndex:mapping_provider_model_unique,where:stage = 'provider',priority:1;uniqueIndex:mapping_consumer_target_unique,where:stage = 'consumer',priority:1"`
	ProfileID          string `gorm:"column:profile_id;not null;default:'';index:mapping_bindings_profile_idx"`
	ProviderID         string `gorm:"column:provider_id;not null;default:'';index:mapping_provider_source_idx,where:stage = 'provider',priority:2;uniqueIndex:mapping_provider_model_unique,where:stage = 'provider',priority:2;uniqueIndex:mapping_consumer_target_unique,where:stage = 'consumer',priority:2"`
	DeviceID           string `gorm:"column:device_id;not null;default:'';index:mapping_provider_source_idx,where:stage = 'provider',priority:3;uniqueIndex:mapping_provider_model_unique,where:stage = 'provider',priority:3;uniqueIndex:mapping_consumer_target_unique,where:stage = 'consumer',priority:3"`
	EndpointID         string `gorm:"column:endpoint_id;not null;default:'';index:mapping_provider_source_idx,where:stage = 'provider',priority:4"`
	CapabilityID       string `gorm:"column:capability_id;not null;default:'';index:mapping_provider_source_idx,where:stage = 'provider',priority:5"`
	PropertyID         string `gorm:"column:property_id;not null;default:'';index:mapping_provider_source_idx,where:stage = 'provider',priority:6"`
	DeviceType         string `gorm:"column:device_type;not null;default:''"`
	ConsumerDeviceType string `gorm:"column:consumer_device_type;not null;default:''"`
	ModelEndpointID    string `gorm:"column:model_endpoint_id;not null;uniqueIndex:mapping_provider_model_unique,where:stage = 'provider',priority:4"`
	ModelCapabilityID  string `gorm:"column:model_capability_id;not null;uniqueIndex:mapping_provider_model_unique,where:stage = 'provider',priority:5"`
	ModelPropertyID    string `gorm:"column:model_property_id;not null;uniqueIndex:mapping_provider_model_unique,where:stage = 'provider',priority:6"`
	ConsumerID         string `gorm:"column:consumer_id;not null;default:'';uniqueIndex:mapping_consumer_target_unique,where:stage = 'consumer',priority:6"`
	TargetID           string `gorm:"column:target_id;not null;default:'';uniqueIndex:mapping_consumer_target_unique,where:stage = 'consumer',priority:4"`
	ConsumerDeviceID   string `gorm:"column:consumer_device_id;not null;default:'';uniqueIndex:mapping_consumer_target_unique,where:stage = 'consumer',priority:5"`
	ConsumerProperty   string `gorm:"column:consumer_property;not null;default:'';uniqueIndex:mapping_consumer_target_unique,where:stage = 'consumer',priority:7"`
	Enabled            bool   `gorm:"column:enabled;not null"`
	CreatedAt          int64  `gorm:"column:created_at;not null"`
	UpdatedAt          int64  `gorm:"column:updated_at;not null"`
}

func (mappingBindingRow) TableName() string { return "mapping_bindings" }

type customModelPropertyRow struct {
	ID           string       `gorm:"column:id;primaryKey"`
	DeviceType   string       `gorm:"column:device_type;not null;uniqueIndex:custom_model_property_path,priority:1;index:custom_model_properties_device_type_idx,priority:1"`
	EndpointID   string       `gorm:"column:endpoint_id;not null;uniqueIndex:custom_model_property_path,priority:2;index:custom_model_properties_device_type_idx,priority:2"`
	CapabilityID string       `gorm:"column:capability_id;not null;uniqueIndex:custom_model_property_path,priority:3;index:custom_model_properties_device_type_idx,priority:3"`
	PropertyID   string       `gorm:"column:property_id;not null;uniqueIndex:custom_model_property_path,priority:4;index:custom_model_properties_device_type_idx,priority:4"`
	DocumentJSON jsonDocument `gorm:"column:document_json;not null"`
	CreatedAt    int64        `gorm:"column:created_at;not null"`
	UpdatedAt    int64        `gorm:"column:updated_at;not null"`
}

func (customModelPropertyRow) TableName() string { return "custom_model_properties" }

type adminUserRow struct {
	ID           uint64 `gorm:"column:id;primaryKey;autoIncrement:false;check:admin_user_singleton,id = 1"`
	Username     string `gorm:"column:username;not null;uniqueIndex"`
	PasswordHash string `gorm:"column:password_hash;not null"`
	CreatedAt    int64  `gorm:"column:created_at;not null"`
	UpdatedAt    int64  `gorm:"column:updated_at;not null"`
}

func (adminUserRow) TableName() string { return "admin_users" }

type adminSessionRow struct {
	TokenHash  string       `gorm:"column:token_hash;primaryKey"`
	AdminID    uint64       `gorm:"column:admin_id;not null"`
	CSRFHash   string       `gorm:"column:csrf_hash;not null"`
	CreatedAt  int64        `gorm:"column:created_at;not null"`
	ExpiresAt  int64        `gorm:"column:expires_at;not null;index:admin_sessions_expires_at_idx"`
	LastSeenAt int64        `gorm:"column:last_seen_at;not null"`
	Admin      adminUserRow `gorm:"foreignKey:AdminID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (adminSessionRow) TableName() string { return "admin_sessions" }

type miotSpecCacheRow struct {
	SpecType     string `gorm:"column:spec_type;primaryKey"`
	Model        string `gorm:"column:model;not null;default:'';index:idx_miot_spec_cache_model"`
	DocumentJSON []byte `gorm:"column:document_json;not null"`
	FetchedAt    int64  `gorm:"column:fetched_at;not null"`
}

func (miotSpecCacheRow) TableName() string { return "miot_spec_cache" }

type customUnifiedModelRow struct {
	DeviceType   string       `gorm:"column:device_type;primaryKey"`
	Name         string       `gorm:"column:name;not null"`
	Version      int          `gorm:"column:version;not null;check:custom_unified_models_version_check,version > 0"`
	DocumentJSON jsonDocument `gorm:"column:document_json;not null"`
	CreatedAt    int64        `gorm:"column:created_at;not null"`
	UpdatedAt    int64        `gorm:"column:updated_at;not null"`
}

func (customUnifiedModelRow) TableName() string { return "custom_unified_models" }

type modelEnumOverrideRow struct {
	ID           string       `gorm:"column:id;primaryKey"`
	DeviceType   string       `gorm:"column:device_type;not null;uniqueIndex:model_enum_override_path,priority:1;index:model_enum_overrides_device_type_idx,priority:1"`
	EndpointID   string       `gorm:"column:endpoint_id;not null;uniqueIndex:model_enum_override_path,priority:2;index:model_enum_overrides_device_type_idx,priority:2"`
	CapabilityID string       `gorm:"column:capability_id;not null;uniqueIndex:model_enum_override_path,priority:3;index:model_enum_overrides_device_type_idx,priority:3"`
	PropertyID   string       `gorm:"column:property_id;not null;uniqueIndex:model_enum_override_path,priority:4;index:model_enum_overrides_device_type_idx,priority:4"`
	DocumentJSON jsonDocument `gorm:"column:document_json;not null"`
	CreatedAt    int64        `gorm:"column:created_at;not null"`
	UpdatedAt    int64        `gorm:"column:updated_at;not null"`
}

func (modelEnumOverrideRow) TableName() string { return "model_enum_overrides" }

func currentModels() []any {
	return []any{
		&providerRow{}, &targetRow{}, &targetVirtualDeviceRow{},
		&matterRuntimeKVRow{}, &matterEndpointIdentityRow{},
		&homeKitAccessoryIDRow{}, &homeKitIIDRow{}, &systemSettingRow{},
		&devicePreferenceRow{}, &auditEventRow{}, &mappingProfileRow{},
		&mappingBindingRow{}, &customModelPropertyRow{}, &modelEnumOverrideRow{}, &adminUserRow{},
		&adminSessionRow{}, &miotSpecCacheRow{}, &customUnifiedModelRow{},
	}
}
