-- #61: a camera's own retention in days; NULL follows the hive-wide
-- retention_days from hive.yaml, 0 keeps footage until space runs out.
ALTER TABLE camera ADD COLUMN retention_days INTEGER;
