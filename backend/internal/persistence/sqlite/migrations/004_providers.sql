CREATE TABLE providers (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 0,
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

INSERT INTO providers(id, type, name, enabled, config_json, created_at, updated_at)
VALUES (
    'virtual-main', 'virtual', 'Virtual Provider', 1, '{}',
    unixepoch('subsec') * 1000, unixepoch('subsec') * 1000
);

