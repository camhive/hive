-- State pushed by the cloud over the channel (ADR-0008): grants, license,
-- token verification keys. Small JSON documents keyed by name; the latest
-- one wins. Read at start so the hive works offline with what it last saw.
CREATE TABLE cloud_state (
    key         TEXT PRIMARY KEY,
    value_json  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
