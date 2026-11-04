ALTER TABLE portal_assets DROP COLUMN IF EXISTS released_version;
ALTER TABLE scripts DROP COLUMN IF EXISTS released_version;
DROP TABLE IF EXISTS application_release_pins;
DROP TABLE IF EXISTS application_releases;
DROP TABLE IF EXISTS application_parts;
DROP TABLE IF EXISTS applications;
