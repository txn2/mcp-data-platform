ALTER TABLE portal_collections
    DROP COLUMN IF EXISTS thumbnail_claimed_until,
    DROP COLUMN IF EXISTS thumbnail_source;

ALTER TABLE resources
    DROP COLUMN IF EXISTS thumbnail_claimed_until,
    DROP COLUMN IF EXISTS thumbnail_failed_at,
    DROP COLUMN IF EXISTS thumbnail_failure,
    DROP COLUMN IF EXISTS thumbnail_renderer;

ALTER TABLE portal_assets
    DROP COLUMN IF EXISTS thumbnail_claimed_until,
    DROP COLUMN IF EXISTS thumbnail_failed_version,
    DROP COLUMN IF EXISTS thumbnail_failure,
    DROP COLUMN IF EXISTS thumbnail_renderer;
