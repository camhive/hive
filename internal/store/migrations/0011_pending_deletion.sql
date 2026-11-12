-- ADR-0016: an owner's deletion that reaches the segment still being
-- written waits here until the engine has closed that file.
CREATE TABLE pending_deletion (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    camera     TEXT NOT NULL,
    from_utc   TEXT NOT NULL,           -- '' = from the beginning
    to_utc     TEXT NOT NULL,
    created_at TEXT NOT NULL
);
