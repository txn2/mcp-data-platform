-- Metadata on script outputs (#1848).
--
-- platform.export(..., metadata={...}) stores a small JSON object on the
-- version it writes, with the run, the script, its version and who asked for
-- the run recorded beside what the script passed. The asset carries the
-- metadata of the latest version that set any, which is what the listing's
-- metadata.<key>=<value> filter reads.
ALTER TABLE portal_asset_versions
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}';

ALTER TABLE portal_assets
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_portal_assets_metadata
    ON portal_assets USING GIN (metadata jsonb_path_ops);
