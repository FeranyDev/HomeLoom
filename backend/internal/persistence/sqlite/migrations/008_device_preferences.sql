CREATE TABLE device_preferences (
    device_id TEXT PRIMARY KEY,
    disabled INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);
