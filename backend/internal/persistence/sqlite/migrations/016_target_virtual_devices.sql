CREATE TABLE target_virtual_devices (
    target_id TEXT NOT NULL,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT '',
    source_device_id TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (target_id, id),
    FOREIGN KEY (target_id) REFERENCES targets(id) ON DELETE CASCADE
);

INSERT INTO target_virtual_devices(target_id, id, name, type, source_device_id, enabled, created_at, updated_at)
SELECT target_id, device_id, device_id, '', device_id, 1,
       unixepoch('subsec') * 1000, unixepoch('subsec') * 1000
FROM target_device_bindings;

DROP TABLE target_device_bindings;
