DROP INDEX IF EXISTS idx_portal_asset_versions_asset_id;
DROP TABLE IF EXISTS portal_asset_versions;
ALTER TABLE portal_assets DROP COLUMN IF EXISTS current_version;
