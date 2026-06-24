-- 000072: knowledge_page thread target (#662 Phase 1)
--
-- Extends the portal_threads 1-of-N target polymorphism (000064) to knowledge
-- pages, so feedback can target a canonical knowledge page the same way it
-- targets assets, collections, and prompts. knowledge_page_id is a TEXT FK to
-- portal_knowledge_pages(id) with ON DELETE CASCADE, mirroring prompt_id's
-- cascade: deleting a page removes its feedback threads. The CHECK constraint is
-- replaced to admit the new target type while keeping the exactly-one-target
-- invariant.
ALTER TABLE portal_threads
    ADD COLUMN IF NOT EXISTS knowledge_page_id TEXT REFERENCES portal_knowledge_pages(id) ON DELETE CASCADE;

ALTER TABLE portal_threads DROP CONSTRAINT IF EXISTS chk_threads_target;
ALTER TABLE portal_threads ADD CONSTRAINT chk_threads_target CHECK (
    (target_type = 'standalone'     AND asset_id IS NULL AND collection_id IS NULL AND prompt_id IS NULL AND knowledge_page_id IS NULL) OR
    (target_type = 'asset'          AND asset_id IS NOT NULL AND collection_id IS NULL AND prompt_id IS NULL AND knowledge_page_id IS NULL) OR
    (target_type = 'collection'     AND asset_id IS NULL AND collection_id IS NOT NULL AND prompt_id IS NULL AND knowledge_page_id IS NULL) OR
    (target_type = 'prompt'         AND asset_id IS NULL AND collection_id IS NULL AND prompt_id IS NOT NULL AND knowledge_page_id IS NULL) OR
    (target_type = 'knowledge_page' AND asset_id IS NULL AND collection_id IS NULL AND prompt_id IS NULL AND knowledge_page_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_portal_threads_knowledge_page_id ON portal_threads(knowledge_page_id)
    WHERE knowledge_page_id IS NOT NULL AND deleted_at IS NULL;
