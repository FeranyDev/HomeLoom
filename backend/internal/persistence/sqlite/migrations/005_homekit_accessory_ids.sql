CREATE TABLE homekit_accessory_ids (
    target_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    aid INTEGER NOT NULL CHECK(aid >= 2),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (target_id, device_id),
    UNIQUE (target_id, aid),
    FOREIGN KEY (target_id) REFERENCES targets(id) ON DELETE CASCADE
);
