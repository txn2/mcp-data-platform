DROP INDEX IF EXISTS idx_portal_assets_metadata;
ALTER TABLE portal_assets DROP COLUMN IF EXISTS metadata;
ALTER TABLE portal_asset_versions DROP COLUMN IF EXISTS metadata;
