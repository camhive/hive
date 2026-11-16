-- ADR-0020: every Event has an end. An instantaneous Event (camera.offline)
-- ends where it began; a lasting one (motion, storage.low) has NULL while it
-- is still going on, and nothing else does. Every row before this migration
-- is instantaneous.
ALTER TABLE event ADD COLUMN end_utc TEXT;
UPDATE event SET end_utc = at_utc;
