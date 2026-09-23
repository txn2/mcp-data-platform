-- Revert 000154: before it there is no column for a script citation, so those
-- rows are removed before the constraint is narrowed back.
DELETE FROM knowledge_page_entity_refs WHERE target_type = 'script';

DROP INDEX IF EXISTS idx_kp_entity_refs_script_rev;
DROP INDEX IF EXISTS uq_kp_entity_refs_script;

ALTER TABLE knowledge_page_entity_refs DROP CONSTRAINT IF EXISTS chk_kp_entity_ref_target;
ALTER TABLE knowledge_page_entity_refs DROP COLUMN IF EXISTS script_id;
ALTER TABLE knowledge_page_entity_refs ADD CONSTRAINT chk_kp_entity_ref_target CHECK (
    (target_type = 'asset'          AND asset_id IS NOT NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL) OR
    (target_type = 'prompt'         AND prompt_id IS NOT NULL AND asset_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL) OR
    (target_type = 'collection'     AND collection_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL) OR
    (target_type = 'knowledge_page' AND ref_page_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL) OR
    (target_type = 'connection'     AND connection_kind IS NOT NULL AND connection_name IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND entity_urn IS NULL) OR
    (target_type = 'datahub'        AND entity_urn IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL)
);
