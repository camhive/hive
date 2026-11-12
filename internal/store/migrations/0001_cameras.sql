-- Cameras attached to this hive. Credentials are AES-GCM encrypted with a key
-- derived from the hive identity key; the database alone reveals nothing.
CREATE TABLE camera (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    grp           TEXT NOT NULL DEFAULT '',
    source_kind   TEXT NOT NULL,
    source_addr   TEXT NOT NULL,
    username      TEXT NOT NULL DEFAULT '',
    password_enc  BLOB,                     -- nonce || ciphertext, NULL when no password
    streams_json  TEXT NOT NULL DEFAULT '{}',
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL,             -- RFC 3339 UTC
    updated_at    TEXT NOT NULL
);
CREATE INDEX camera_name ON camera(name);
