-- #147, ADR-0019 addendum: the owner's automatic-lock settings for the
-- encrypted archive, in seconds; 0 is off. No row means both off.
CREATE TABLE archive_autolock (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    after_s     INTEGER NOT NULL CHECK (after_s >= 0),
    away_s      INTEGER NOT NULL CHECK (away_s >= 0),
    updated_at  TEXT NOT NULL
);
