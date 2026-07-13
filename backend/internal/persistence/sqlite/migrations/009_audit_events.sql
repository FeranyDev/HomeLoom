CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    correlation_id TEXT NOT NULL,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL,
    route TEXT NOT NULL,
    status INTEGER NOT NULL,
    outcome TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX audit_events_created_at_idx ON audit_events(created_at DESC);
CREATE INDEX audit_events_correlation_id_idx ON audit_events(correlation_id);
