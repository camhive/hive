-- #157: a camera's motion sensitivity, low, normal or high.
ALTER TABLE camera ADD COLUMN sensitivity TEXT NOT NULL DEFAULT 'normal';
