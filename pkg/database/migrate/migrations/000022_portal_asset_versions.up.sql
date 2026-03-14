-- Add version tracking to portal_assets
ALTER TABLE portal_assets ADD COLUMN current_version INT NOT NULL DEFAULT 1;

-- Version history table
CREATE TABLE portal_asset_versions (
    id            TEXT        PRIMARY KEY,
    asset_id      TEXT        NOT NULL REFERENCES portal_assets(id),
    version       INT         NOT NULL,
    s3_key        TEXT        NOT NULL,
    s3_bucket     TEXT        NOT NULL,
    content_type  TEXT        NOT NULL,
    size_bytes    BIGINT      NOT NULL,
    created_by    TEXT        NOT NULL DEFAULT '',
    change_summary TEXT       NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(asset_id, version)
);

CREATE INDEX idx_portal_asset_versions_asset_id ON portal_asset_versions(asset_id);

-- Backfill v1 for existing assets
INSERT INTO portal_asset_versions (id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes, created_by, change_summary, created_at)
SELECT
    id || '-v1',
    id,
    1,
    s3_key,
    s3_bucket,
    content_type,
    size_bytes,
    owner_id,
    'Initial version',
    created_at
FROM portal_assets
WHERE deleted_at IS NULL;
