CREATE TABLE targets (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 0,
    address TEXT NOT NULL DEFAULT '',
    pin TEXT NOT NULL DEFAULT '',
    setup_id TEXT NOT NULL DEFAULT '',
    store_path TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX targets_enabled_address
    ON targets(address) WHERE enabled = 1 AND address <> '';
CREATE UNIQUE INDEX targets_enabled_setup_id
    ON targets(setup_id) WHERE enabled = 1 AND setup_id <> '';
CREATE UNIQUE INDEX targets_enabled_store_path
    ON targets(store_path) WHERE enabled = 1 AND store_path <> '';

CREATE TABLE target_device_bindings (
    target_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    PRIMARY KEY (target_id, device_id),
    FOREIGN KEY (target_id) REFERENCES targets(id) ON DELETE CASCADE
);

INSERT INTO targets(
    id, type, name, enabled, address, pin, setup_id, store_path, created_at, updated_at
) VALUES (
    'apple-main', 'apple-hap', 'HomeLoom 主桥', 1, ':51826', '00102003', 'HLM1',
    './data/hap/apple-main', unixepoch('subsec') * 1000, unixepoch('subsec') * 1000
);

