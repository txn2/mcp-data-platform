DROP INDEX IF EXISTS idx_webhook_rejections_source_outcome;
ALTER TABLE webhook_rejections
    DROP COLUMN IF EXISTS first_at,
    DROP COLUMN IF EXISTS count;
