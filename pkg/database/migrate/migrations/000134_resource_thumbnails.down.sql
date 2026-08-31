ALTER TABLE resources DROP COLUMN IF EXISTS thumbnail_dark_captured_at;
ALTER TABLE resources DROP COLUMN IF EXISTS thumbnail_captured_at;
ALTER TABLE resources DROP COLUMN IF EXISTS thumbnail_dark_s3_key;
ALTER TABLE resources DROP COLUMN IF EXISTS thumbnail_s3_key;
