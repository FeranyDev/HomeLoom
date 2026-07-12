CREATE TABLE property_states (
    device_id TEXT NOT NULL,
    property_id TEXT NOT NULL,
    bool_value INTEGER,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (device_id, property_id)
);

