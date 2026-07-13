CREATE TABLE mapping_profiles (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK(kind IN ('provider', 'capability', 'target')),
    version INTEGER NOT NULL CHECK(version > 0),
    document_json TEXT NOT NULL CHECK(json_valid(document_json)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX mapping_profiles_kind_id_idx ON mapping_profiles(kind, id);
