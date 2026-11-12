-- Which version of the hive's reading of a segment file filled its row
-- (duration, and later what is derived from the file). A row read by an
-- older version is read again; 0 = before versions existed.
ALTER TABLE segment ADD COLUMN analysis INTEGER NOT NULL DEFAULT 0;
