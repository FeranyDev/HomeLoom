CREATE TABLE miot_spec_cache (
    spec_type TEXT PRIMARY KEY,
    model TEXT NOT NULL DEFAULT '',
    document_json BLOB NOT NULL,
    fetched_at INTEGER NOT NULL
);

CREATE INDEX idx_miot_spec_cache_model ON miot_spec_cache(model);
