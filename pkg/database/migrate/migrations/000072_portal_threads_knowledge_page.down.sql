-- Revert 000072: drop the knowledge_page thread target.
-- Remove knowledge_page-targeted threads first so the restored CHECK (which has
-- no knowledge_page branch) holds.
DELETE FROM portal_threads WHERE target_type = 'knowledge_page';

ALTER TABLE portal_threads DROP CONSTRAINT IF EXISTS chk_threads_target;
ALTER TABLE portal_threads ADD CONSTRAINT chk_threads_target CHECK (
    (target_type = 'standalone' AND asset_id IS NULL AND collection_id IS NULL AND prompt_id IS NULL) OR
    (target_type = 'asset'      AND asset_id IS NOT NULL AND collection_id IS NULL AND prompt_id IS NULL) OR
    (target_type = 'collection' AND asset_id IS NULL AND collection_id IS NOT NULL AND prompt_id IS NULL) OR
    (target_type = 'prompt'     AND asset_id IS NULL AND collection_id IS NULL AND prompt_id IS NOT NULL)
);

DROP INDEX IF EXISTS idx_portal_threads_knowledge_page_id;
ALTER TABLE portal_threads DROP COLUMN IF EXISTS knowledge_page_id;
