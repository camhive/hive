-- ADR-0017: what a camera is (MAC, ONVIF endpoint, serial, make, model) and
-- whether it sits in one of the hive's networks. Known cameras learn these on
-- their next connect.
ALTER TABLE camera ADD COLUMN identity_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE camera ADD COLUMN network TEXT NOT NULL DEFAULT '';
