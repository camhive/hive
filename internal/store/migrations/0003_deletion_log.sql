-- Every archive deletion the hive performs (ADR-0009 / ADR-0011): retention
-- is the only code path that removes footage, and it leaves a trace.
CREATE TABLE deletion_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    deleted_at TEXT NOT NULL,
    camera     TEXT NOT NULL,
    stream     TEXT NOT NULL,
    start_utc  TEXT NOT NULL,
    bytes      INTEGER NOT NULL,
    reason     TEXT NOT NULL            -- age | space
);
