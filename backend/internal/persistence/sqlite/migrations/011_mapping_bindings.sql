CREATE TABLE mapping_bindings (
    id TEXT PRIMARY KEY,
    profile_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    endpoint_id TEXT NOT NULL,
    capability_id TEXT NOT NULL,
    property_id TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (provider_id, device_id, endpoint_id, capability_id, property_id)
);

CREATE INDEX mapping_bindings_profile_idx ON mapping_bindings(profile_id);
