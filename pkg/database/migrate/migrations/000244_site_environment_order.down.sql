-- The prod environments the upgrade added are kept: one may have been
-- promoted to since.
ALTER TABLE site_environments DROP COLUMN IF EXISTS position;
