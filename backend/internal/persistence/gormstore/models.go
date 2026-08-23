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

type mediaSourceRow struct {
	DeviceID         string               `gorm:"column:device_id;primaryKey"`
	ProviderID       string               `gorm:"column:provider_id;not null;index:media_sources_provider_id_idx"`
	ProviderDeviceID string               `gorm:"column:provider_device_id;not null;default:''"`
	Protocol         string               `gorm:"column:protocol;not null;check:media_sources_protocol_check,protocol <> ''"`
	CredentialRef    string               `gorm:"column:credential_ref;not null;default:''"`
	ProfilesJSON     jsonDocument         `gorm:"column:profiles_json;not null;default:'[]'"`
	SourceConfigJSON jsonDocument         `gorm:"column:source_config_json;not null;default:'{}'"`
	Revision         uint64               `gorm:"column:revision;not null;check:media_sources_revision_check,revision > 0"`
	Enabled          bool                 `gorm:"column:enabled;not null;default:false"`
	CreatedAt        int64                `gorm:"column:created_at;not null"`
	UpdatedAt        int64                `gorm:"column:updated_at;not null"`
	Provider         providerRow          `gorm:"foreignKey:ProviderID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
	Credentials      []mediaCredentialRow `gorm:"foreignKey:DeviceID;references:DeviceID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
	Streams          []mediaStreamRow     `gorm:"foreignKey:DeviceID;references:DeviceID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
	AuthLeases       []mediaAuthLeaseRow  `gorm:"foreignKey:DeviceID;references:DeviceID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (mediaSourceRow) TableName() string { return "media_sources" }

type mediaCredentialRow struct {
	ID                      string `gorm:"column:id;primaryKey"`
	DeviceID                string `gorm:"column:device_id;not null;index:media_credentials_device_id_idx"`
	CredentialType          string `gorm:"column:credential_type;not null;check:media_credentials_type_check,credential_type IN ('static_password','homekit_input_pairing_identity','homekit_output_accessory_identity','device_secret','vendor_device_material')"`
	CredentialBlobEncrypted string `gorm:"column:credential_blob_encrypted;not null"`
	KeyVersion              uint32 `gorm:"column:key_version;not null;default:1;check:media_credentials_key_version_check,key_version > 0"`
	Version                 uint64 `gorm:"column:version;not null;default:1;check:media_credentials_version_check,version > 0"`
	Status                  string `gorm:"column:status;not null;default:'active';check:media_credentials_status_check,status IN ('active','disabled','revoked')"`
	CreatedAt               int64  `gorm:"column:created_at;not null"`
	UpdatedAt               int64  `gorm:"column:updated_at;not null"`
}

func (mediaCredentialRow) TableName() string { return "media_credentials" }

type mediaStreamRow struct {
	ID              string       `gorm:"column:id;primaryKey"`
	DeviceID        string       `gorm:"column:device_id;not null;index:media_streams_device_id_idx"`
	Protocol        string       `gorm:"column:protocol;not null;check:media_streams_protocol_check,protocol <> ''"`
	CredentialRef   string       `gorm:"column:credential_ref;not null;default:''"`
	Profile         string       `gorm:"column:profile;not null;default:''"`
	Mode            string       `gorm:"column:mode;not null;check:media_streams_mode_check,mode IN ('on_demand','preload','always_on')"`
	AudioEnabled    bool         `gorm:"column:audio_enabled;not null;default:false"`
	TalkbackEnabled bool         `gorm:"column:talkback_enabled;not null;default:false"`
	OptionsJSON     jsonDocument `gorm:"column:options_json;not null;default:'{}'"`
	Revision        uint64       `gorm:"column:revision;not null;check:media_streams_revision_check,revision > 0"`
	Enabled         bool         `gorm:"column:enabled;not null;default:false"`
	CreatedAt       int64        `gorm:"column:created_at;not null"`
	UpdatedAt       int64        `gorm:"column:updated_at;not null"`
}

func (mediaStreamRow) TableName() string { return "media_streams" }

type mediaRuntimeKVRow struct {
	Namespace string `gorm:"column:namespace;primaryKey"`
	Key       string `gorm:"column:key;primaryKey"`
	Value     string `gorm:"column:value_encrypted;not null"`
	Sensitive bool   `gorm:"column:sensitive;not null;default:true"`
	UpdatedAt int64  `gorm:"column:updated_at;not null"`
}

func (mediaRuntimeKVRow) TableName() string { return "media_runtime_kv" }

type mediaAuthLeaseRow struct {
	ID                  string `gorm:"column:id;primaryKey"`
	WorkerID            string `gorm:"column:worker_id;not null;index:media_auth_leases_worker_idx,priority:1;uniqueIndex:media_auth_leases_request_unique,priority:1"`
	WorkerInstanceID    string `gorm:"column:worker_instance_id;not null;index:media_auth_leases_worker_idx,priority:2;uniqueIndex:media_auth_leases_request_unique,priority:2"`
	DeviceID            string `gorm:"column:device_id;not null;index:media_auth_leases_device_id_idx"`
	Protocol            string `gorm:"column:protocol;not null;check:media_auth_leases_protocol_check,protocol <> ''"`
	Purpose             string `gorm:"column:purpose;not null;check:media_auth_leases_purpose_check,purpose <> ''"`
	Status              string `gorm:"column:status;not null;check:media_auth_leases_status_check,status IN ('claimed','connected','ended','expired','revoked','failed')"`
	ExpiresAt           int64  `gorm:"column:expires_at;not null;index:media_auth_leases_expires_at_idx"`
	RequestID           string `gorm:"column:request_id;not null;uniqueIndex:media_auth_leases_request_unique,priority:3"`
	RequestMaterialHash string `gorm:"column:request_material_hash;not null;default:''"`
	MaxUses             uint32 `gorm:"column:max_uses;not null;default:1;check:media_auth_leases_max_uses_check,max_uses > 0"`
	UseCount            uint32 `gorm:"column:use_count;not null;default:0;check:media_auth_leases_use_count_check,use_count <= max_uses"`
	CreatedAt           int64  `gorm:"column:created_at;not null"`
	ClaimedAt           int64  `gorm:"column:claimed_at;not null;default:0"`
	UsedAt              int64  `gorm:"column:used_at;not null;default:0"`
	EndedAt             int64  `gorm:"column:ended_at;not null;default:0"`
}

func (mediaAuthLeaseRow) TableName() string { return "media_auth_leases" }

type mediaAuthAuditRow struct {
	ID             int64  `gorm:"column:id;primaryKey;autoIncrement"`
	WorkerID       string `gorm:"column:worker_id;not null;index:media_auth_audit_worker_id_idx"`
	DeviceID       string `gorm:"column:device_id;not null;index:media_auth_audit_device_id_idx"`
	Provider       string `gorm:"column:provider;not null;default:''"`
	Action         string `gorm:"column:action;not null"`
	Result         string `gorm:"column:result;not null"`
	ErrorCode      string `gorm:"column:error_code;not null;default:''"`
	RemoteIdentity string `gorm:"column:remote_identity;not null;default:''"`
	CreatedAt      int64  `gorm:"column:created_at;not null;index:media_auth_audit_created_at_idx,sort:desc"`
}

func (mediaAuthAuditRow) TableName() string { return "media_auth_audit" }

type mediaConfigStateRow struct {
	ID         uint8  `gorm:"column:id;primaryKey;autoIncrement:false;check:media_config_state_singleton_check,id = 1"`
	Generation uint64 `gorm:"column:generation;not null;check:media_config_state_generation_check,generation > 0"`
	Revision   uint64 `gorm:"column:revision;not null;check:media_config_state_revision_check,revision > 0"`
	UpdatedAt  int64  `gorm:"column:updated_at;not null"`
}

func (mediaConfigStateRow) TableName() string { return "media_config_state" }

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

// MCP configuration is intentionally separate from Provider snapshots. A
// Provider can rediscover a device without overwriting the administrator's AI
// exposure policy or operational notes.
type mcpDeviceConfigRow struct {
	DeviceID      string `gorm:"column:device_id;primaryKey"`
	Enabled       bool   `gorm:"column:enabled;not null;default:false"`
	UsageNote     string `gorm:"column:usage_note;not null;default:''"`
	DefaultAccess string `gorm:"column:default_access;not null;default:'hidden'"`
	CreatedAt     int64  `gorm:"column:created_at;not null"`
	UpdatedAt     int64  `gorm:"column:updated_at;not null"`
}

func (mcpDeviceConfigRow) TableName() string { return "mcp_device_configs" }

type mcpPropertyConfigRow struct {
	DeviceID     string `gorm:"column:device_id;primaryKey"`
	EndpointID   string `gorm:"column:endpoint_id;primaryKey"`
	CapabilityID string `gorm:"column:capability_id;primaryKey"`
	PropertyID   string `gorm:"column:property_id;primaryKey"`
	UsageNote    string `gorm:"column:usage_note;not null;default:''"`
	Access       string `gorm:"column:access;not null;default:'inherit'"`
	CreatedAt    int64  `gorm:"column:created_at;not null"`
	UpdatedAt    int64  `gorm:"column:updated_at;not null"`
}

func (mcpPropertyConfigRow) TableName() string { return "mcp_property_configs" }

// aiAutomationRow stores a whole task document. Task definitions evolve with
// their trigger form, while the document never contains an AI provider key.
type aiAutomationRow struct {
	ID           string       `gorm:"column:id;primaryKey"`
	DocumentJSON jsonDocument `gorm:"column:document_json;not null"`
	CreatedAt    int64        `gorm:"column:created_at;not null"`
	UpdatedAt    int64        `gorm:"column:updated_at;not null"`
}

func (aiAutomationRow) TableName() string { return "ai_automations" }

// logicalDeviceRow stores the complete validated configuration in one document.
// Routes evolve as a unit and are not queried independently, so a document keeps
// PostgreSQL and SQLite migration behaviour identical without duplicating the
// route validation rules in the persistence layer.
type logicalDeviceRow struct {
	ID           string       `gorm:"column:id;primaryKey"`
	DocumentJSON jsonDocument `gorm:"column:document_json;not null"`
	CreatedAt    int64        `gorm:"column:created_at;not null"`
	UpdatedAt    int64        `gorm:"column:updated_at;not null"`
}

func (logicalDeviceRow) TableName() string { return "logical_devices" }

// providerDeviceIdentityRow binds a Provider-native device identifier to the
// canonical Device.ID published by HomeLoom. The row deliberately has no
// provider foreign key: disabling or deleting a Provider must not erase a
// stable device identity that can be restored on a later reconfiguration.
type providerDeviceIdentityRow struct {
	ProviderID       string `gorm:"column:provider_id;primaryKey"`
	ProviderDeviceID string `gorm:"column:provider_device_id;primaryKey"`
	DeviceID         string `gorm:"column:device_id;not null;uniqueIndex:provider_device_identity_device,priority:2"`
	CreatedAt        int64  `gorm:"column:created_at;not null"`
	UpdatedAt        int64  `gorm:"column:updated_at;not null"`
}

func (providerDeviceIdentityRow) TableName() string { return "provider_device_identities" }

// logicalDeviceIdentityRow reserves logical IDs after an explicit unlink.
// This distinguishes an operator deletion from a transient Provider outage and
// keeps Target-facing identities stable during the retention window.
type logicalDeviceIdentityRow struct {
	LogicalDeviceID string `gorm:"column:logical_device_id;primaryKey"`
	DeletedAt       int64  `gorm:"column:deleted_at;not null;default:0;index:logical_device_identity_purge_idx"`
	PurgeAfter      int64  `gorm:"column:purge_after;not null;default:0;index:logical_device_identity_purge_idx"`
	CreatedAt       int64  `gorm:"column:created_at;not null"`
	UpdatedAt       int64  `gorm:"column:updated_at;not null"`
}

func (logicalDeviceIdentityRow) TableName() string { return "logical_device_identities" }

// deviceCapabilityIdentityRow preserves the stable endpoint/capability path
// independently of mutable labels or discovery ordering. Rows are append-only
// until an explicit retention purge so a temporarily absent capability does
// not receive a new identity when it returns.
type deviceCapabilityIdentityRow struct {
	DeviceID     string `gorm:"column:device_id;primaryKey"`
	EndpointID   string `gorm:"column:endpoint_id;primaryKey"`
	CapabilityID string `gorm:"column:capability_id;primaryKey"`
	CreatedAt    int64  `gorm:"column:created_at;not null"`
	UpdatedAt    int64  `gorm:"column:updated_at;not null"`
}

func (deviceCapabilityIdentityRow) TableName() string { return "device_capability_identities" }

// homeKitAccessoryUUIDRow supplies a durable opaque accessory identity in
// addition to HAP's AID/IID allocation. It is scoped to the Target and is
// removed only when that Target itself is explicitly deleted.
type homeKitAccessoryUUIDRow struct {
	TargetID  string    `gorm:"column:target_id;primaryKey;uniqueIndex:homekit_accessory_uuid_target_uuid,priority:1"`
	DeviceID  string    `gorm:"column:device_id;primaryKey"`
	UUID      string    `gorm:"column:uuid;not null;uniqueIndex:homekit_accessory_uuid_target_uuid,priority:2"`
	CreatedAt int64     `gorm:"column:created_at;not null"`
	Target    targetRow `gorm:"foreignKey:TargetID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (homeKitAccessoryUUIDRow) TableName() string { return "homekit_accessory_uuids" }

type deviceLocationPreferenceRow struct {
	DeviceID  string `gorm:"column:device_id;primaryKey"`
	HomeID    string `gorm:"column:home_id;not null"`
	HomeName  string `gorm:"column:home_name;not null;index:device_location_home_room_idx,priority:1"`
	RoomID    string `gorm:"column:room_id;not null;default:''"`
	RoomName  string `gorm:"column:room_name;not null;default:'';index:device_location_home_room_idx,priority:2"`
	CreatedAt int64  `gorm:"column:created_at;not null"`
	UpdatedAt int64  `gorm:"column:updated_at;not null"`
}

func (deviceLocationPreferenceRow) TableName() string { return "device_location_preferences" }

type deviceLocationHomeRow struct {
	ID        string                  `gorm:"column:id;primaryKey"`
	Name      string                  `gorm:"column:name;not null;uniqueIndex"`
	CreatedAt int64                   `gorm:"column:created_at;not null"`
	UpdatedAt int64                   `gorm:"column:updated_at;not null"`
	Rooms     []deviceLocationRoomRow `gorm:"foreignKey:HomeID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (deviceLocationHomeRow) TableName() string { return "device_location_homes" }

type deviceLocationRoomRow struct {
	ID        string                `gorm:"column:id;primaryKey"`
	HomeID    string                `gorm:"column:home_id;not null;uniqueIndex:device_location_room_name,priority:1;index"`
	Name      string                `gorm:"column:name;not null;uniqueIndex:device_location_room_name,priority:2"`
	CreatedAt int64                 `gorm:"column:created_at;not null"`
	UpdatedAt int64                 `gorm:"column:updated_at;not null"`
	Home      deviceLocationHomeRow `gorm:"foreignKey:HomeID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (deviceLocationRoomRow) TableName() string { return "device_location_rooms" }

type auditEventRow struct {
	ID            int64        `gorm:"column:id;primaryKey;autoIncrement"`
	CorrelationID string       `gorm:"column:correlation_id;not null;index:audit_events_correlation_id_idx"`
	Actor         string       `gorm:"column:actor;not null"`
	Action        string       `gorm:"column:action;not null"`
	ResourceType  string       `gorm:"column:resource_type;not null"`
	ResourceID    string       `gorm:"column:resource_id;not null;default:''"`
	Method        string       `gorm:"column:method;not null"`
	Route         string       `gorm:"column:route;not null"`
	Status        int          `gorm:"column:status;not null"`
	Outcome       string       `gorm:"column:outcome;not null"`
	DetailsJSON   jsonDocument `gorm:"column:details_json;not null;default:'[]'"`
	CreatedAt     int64        `gorm:"column:created_at;not null;index:audit_events_created_at_idx,sort:desc"`
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
		&providerRow{}, &targetRow{}, &targetVirtualDeviceRow{}, &mediaSourceRow{},
		&mediaCredentialRow{}, &mediaStreamRow{}, &mediaRuntimeKVRow{},
		&mediaAuthLeaseRow{}, &mediaAuthAuditRow{}, &mediaConfigStateRow{},
		&matterRuntimeKVRow{}, &matterEndpointIdentityRow{},
		&homeKitAccessoryIDRow{}, &homeKitIIDRow{}, &homeKitAccessoryUUIDRow{}, &systemSettingRow{},
		&devicePreferenceRow{}, &mcpDeviceConfigRow{}, &mcpPropertyConfigRow{}, &aiAutomationRow{}, &logicalDeviceRow{}, &providerDeviceIdentityRow{}, &logicalDeviceIdentityRow{}, &deviceCapabilityIdentityRow{}, &deviceLocationHomeRow{}, &deviceLocationRoomRow{}, &deviceLocationPreferenceRow{}, &auditEventRow{}, &mappingProfileRow{},
		&mappingBindingRow{}, &customModelPropertyRow{}, &modelEnumOverrideRow{}, &adminUserRow{},
		&adminSessionRow{}, &miotSpecCacheRow{}, &customUnifiedModelRow{},
	}
}
