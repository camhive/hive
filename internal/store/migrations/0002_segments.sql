-- Archive index (ADR-0009). Files on disk are the truth; rows describe them
-- and are reconciled against the filesystem on start and periodically.
CREATE TABLE segment (
    camera      TEXT NOT NULL,
    stream      TEXT NOT NULL,
    start_utc   TEXT NOT NULL,          -- RFC 3339 with microseconds, from the file name
    duration_ms INTEGER NOT NULL,       -- 0 while the file is still being written or unparsable
    bytes       INTEGER NOT NULL,
    path        TEXT NOT NULL,          -- absolute path
    complete    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (camera, stream, start_utc)
);
CREATE INDEX segment_time ON segment(camera, stream, start_utc);
