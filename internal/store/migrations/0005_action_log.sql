-- Local audit trail of what happened on this hive (ADR-0011): who changed
-- what, exports produced, grants and license applied, updates installed.
-- Append-only; shown to the owner as "Activity".
CREATE TABLE action_log (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    at_utc       TEXT NOT NULL,
    actor        TEXT NOT NULL,            -- account id, "system" or "cloud"
    action       TEXT NOT NULL,            -- camera.add | camera.update | camera.remove | export.done | ...
    target       TEXT NOT NULL DEFAULT '', -- camera id, export id ...
    details_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX action_log_time ON action_log(at_utc);
