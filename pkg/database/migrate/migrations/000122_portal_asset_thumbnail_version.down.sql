-- Reverse 000122. With the columns gone nothing can tell a current capture from
-- one left behind by a version write, so a downgraded deployment is back to
-- treating any recorded thumbnail as current until the write path blanks it.
ALTER TABLE portal_assets
    DROP COLUMN IF EXISTS thumbnail_dark_version;

ALTER TABLE portal_assets
    DROP COLUMN IF EXISTS thumbnail_version;
