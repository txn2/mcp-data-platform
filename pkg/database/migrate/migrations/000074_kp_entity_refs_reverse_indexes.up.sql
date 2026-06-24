-- Reverse-lookup indexes on knowledge_page_entity_refs (#664 Phase 4): the
-- Phase 0 unique indexes are keyed (page_id, <target>) for the forward lookup
-- (a page's refs); these target-column indexes make the reverse lookup (the
-- pages that reference a given entity) indexed for every target type.
CREATE INDEX IF NOT EXISTS idx_kp_entity_refs_asset_rev
    ON knowledge_page_entity_refs (asset_id) WHERE asset_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_kp_entity_refs_prompt_rev
    ON knowledge_page_entity_refs (prompt_id) WHERE prompt_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_kp_entity_refs_collection_rev
    ON knowledge_page_entity_refs (collection_id) WHERE collection_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_kp_entity_refs_ref_page_rev
    ON knowledge_page_entity_refs (ref_page_id) WHERE ref_page_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_kp_entity_refs_connection_rev
    ON knowledge_page_entity_refs (connection_kind, connection_name) WHERE connection_kind IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_kp_entity_refs_urn_rev
    ON knowledge_page_entity_refs (entity_urn) WHERE entity_urn IS NOT NULL;
