-- ADR-0015: sound is recorded only when the owner switches it on. Existing
-- cameras start with it off.
ALTER TABLE camera ADD COLUMN record_audio INTEGER NOT NULL DEFAULT 0;
