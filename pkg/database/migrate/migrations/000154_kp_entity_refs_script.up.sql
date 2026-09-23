-- 000154: a knowledge page may cite a managed script (#1855).
--
-- Some pages exist to say which script produces or maintains a dataset, so the
-- script is the thing they most need to cite. A script is personal to its owner
-- (000119), which makes the citation readable by its owner and administrators
-- only. That is decided per reader when the page's references are resolved, the
-- same way an asset or prompt the reader cannot open is, rather than by refusing
-- the citation for everyone at write time.
--
-- script_id is a real foreign key like the other internal targets: a deleted
-- script takes its citations with it, and a citation to a script that does not
-- exist is refused by the database as well as by the write path's existence
-- check.
ALTER TABLE knowledge_page_entity_refs
    ADD COLUMN IF NOT EXISTS script_id UUID REFERENCES scripts(id) ON DELETE CASCADE;

ALTER TABLE knowledge_page_entity_refs DROP CONSTRAINT IF EXISTS chk_kp_entity_ref_target;
ALTER TABLE knowledge_page_entity_refs ADD CONSTRAINT chk_kp_entity_ref_target CHECK (
    (target_type = 'asset'          AND asset_id IS NOT NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'prompt'         AND prompt_id IS NOT NULL AND asset_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'collection'     AND collection_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'knowledge_page' AND ref_page_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'connection'     AND connection_kind IS NOT NULL AND connection_name IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'datahub'        AND entity_urn IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND script_id IS NULL) OR
    (target_type = 'script'         AND script_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL)
);

-- Recorded once per page (the union on promotion is ON CONFLICT against this),
-- and indexed by target for the reverse lookup, like every other target column.
CREATE UNIQUE INDEX IF NOT EXISTS uq_kp_entity_refs_script
    ON knowledge_page_entity_refs(page_id, script_id) WHERE script_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_kp_entity_refs_script_rev
    ON knowledge_page_entity_refs (script_id) WHERE script_id IS NOT NULL;
