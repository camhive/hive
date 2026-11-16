-- The owner's local emergency login (#57, ADR-0004): one username and an
-- argon2id hash (PHC string). The row exists only while the login is
-- enabled; disabling deletes it. Never sent to the cloud.
CREATE TABLE local_login (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    username       TEXT NOT NULL,
    password_hash  TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
