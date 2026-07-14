ALTER TABLE mapping_bindings RENAME TO mapping_bindings_legacy;

CREATE TABLE mapping_bindings (
    id TEXT PRIMARY KEY,
    stage TEXT NOT NULL CHECK(stage IN ('provider', 'consumer')),
    profile_id TEXT NOT NULL DEFAULT '',
    provider_id TEXT NOT NULL DEFAULT '',
    device_id TEXT NOT NULL DEFAULT '',
    endpoint_id TEXT NOT NULL DEFAULT '',
    capability_id TEXT NOT NULL DEFAULT '',
    property_id TEXT NOT NULL DEFAULT '',
    device_type TEXT NOT NULL DEFAULT '',
    model_endpoint_id TEXT NOT NULL,
    model_capability_id TEXT NOT NULL,
    model_property_id TEXT NOT NULL,
    consumer_id TEXT NOT NULL DEFAULT '',
    consumer_property TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

INSERT INTO mapping_bindings(
    id, stage, profile_id, provider_id, device_id, endpoint_id,
    capability_id, property_id, model_endpoint_id, model_capability_id,
    model_property_id, enabled, created_at, updated_at
)
SELECT id, 'provider', profile_id, provider_id, device_id, endpoint_id,
       capability_id, property_id, endpoint_id, capability_id, property_id,
       enabled, created_at, updated_at
FROM mapping_bindings_legacy;

DROP TABLE mapping_bindings_legacy;

CREATE UNIQUE INDEX mapping_provider_source_unique
    ON mapping_bindings(provider_id, device_id, endpoint_id, capability_id, property_id)
    WHERE stage = 'provider';
CREATE UNIQUE INDEX mapping_provider_model_unique
    ON mapping_bindings(provider_id, device_id, model_endpoint_id, model_capability_id, model_property_id)
    WHERE stage = 'provider';
CREATE UNIQUE INDEX mapping_consumer_target_unique
    ON mapping_bindings(provider_id, device_id, consumer_id, consumer_property)
    WHERE stage = 'consumer';
CREATE INDEX mapping_bindings_profile_idx ON mapping_bindings(profile_id);

CREATE TABLE custom_model_properties (
    id TEXT PRIMARY KEY,
    device_type TEXT NOT NULL,
    endpoint_id TEXT NOT NULL,
    capability_id TEXT NOT NULL,
    property_id TEXT NOT NULL,
    document_json TEXT NOT NULL CHECK(json_valid(document_json)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(device_type, endpoint_id, capability_id, property_id)
);

CREATE INDEX custom_model_properties_device_type_idx
    ON custom_model_properties(device_type, endpoint_id, capability_id, property_id);
