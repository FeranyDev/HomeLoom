CREATE TABLE homekit_iids (
    target_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    resource_key TEXT NOT NULL,
    iid INTEGER NOT NULL CHECK(iid >= 1),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (target_id, device_id, resource_key),
    UNIQUE (target_id, device_id, iid),
    FOREIGN KEY (target_id) REFERENCES targets(id) ON DELETE CASCADE
);
