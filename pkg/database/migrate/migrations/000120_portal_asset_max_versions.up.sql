-- 000120: a per-asset cap on how much version history an asset keeps (#1421).
--
-- portal_asset_versions had INSERT and SELECT and nothing else: no sweeper, no
-- configuration, and an asset delete that is a soft delete, so the ON DELETE
-- CASCADE in 000022 never fires. History was kept forever, rows and objects
-- alike. That was tolerable while a version meant a person edited a page; a
-- managed script on an hourly schedule writes 24 versions a day to one asset.
--
-- The column is nullable and that is the point of it. NULL means the asset has
-- no opinion and inherits the deployment's portal.max_versions (100 unless
-- configured), which is the state every existing row is left in -- an upgrade
-- that silently pinned every asset to whatever the platform default happened to
-- be at migration time would be a decision the operator never made. 0 means the
-- asset keeps every version, N means it keeps the newest N.
--
-- The CHECK is the last line of defence, not the first: every entry point
-- (portal PUT, admin PUT, manage_asset) refuses a negative value with a 400 or
-- a tool error naming the field. The constraint is here so a value that reached
-- the column another way is still not a silently-inverted retention rule.
ALTER TABLE portal_assets
    ADD COLUMN IF NOT EXISTS max_versions INTEGER;

ALTER TABLE portal_assets
    DROP CONSTRAINT IF EXISTS portal_assets_max_versions_nonneg;

ALTER TABLE portal_assets
    ADD CONSTRAINT portal_assets_max_versions_nonneg
    CHECK (max_versions IS NULL OR max_versions >= 0);
