DELETE FROM site_releases WHERE manifest_parent IS NOT NULL;
ALTER TABLE site_release_files DROP COLUMN IF EXISTS stored_in;
ALTER TABLE site_release_files DROP COLUMN IF EXISTS removed;
DROP INDEX IF EXISTS idx_site_releases_manifest_parent;
ALTER TABLE site_releases DROP COLUMN IF EXISTS manifest_depth;
ALTER TABLE site_releases DROP COLUMN IF EXISTS manifest_parent;
ALTER TABLE site_releases DROP COLUMN IF EXISTS base_number;
