CREATE TABLE custom_unified_models (
    device_type TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version > 0),
    document_json TEXT NOT NULL CHECK(json_valid(document_json)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
