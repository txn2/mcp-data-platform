ALTER TABLE portal_collections
    DROP COLUMN IF EXISTS thumbnail_failed_source,
    DROP COLUMN IF EXISTS thumbnail_failure,
    DROP COLUMN IF EXISTS thumbnail_attempts;

ALTER TABLE resources DROP COLUMN IF EXISTS thumbnail_attempts;

ALTER TABLE portal_assets DROP COLUMN IF EXISTS thumbnail_attempts;
