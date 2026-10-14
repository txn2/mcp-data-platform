-- A citation the rewrite turned into a catalog reference has no DataHub form
-- to return to; it is dropped with the target type that described it.
DELETE FROM knowledge_page_entity_refs WHERE target_type = 'catalog';
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

DROP TABLE IF EXISTS catalog_import_map;
DROP TABLE IF EXISTS catalog_imports;

DELETE FROM catalog_lineage WHERE writer_kind = 'import';
ALTER TABLE catalog_lineage DROP CONSTRAINT IF EXISTS catalog_lineage_writer_kind_check;
ALTER TABLE catalog_lineage ADD CONSTRAINT catalog_lineage_writer_kind_check
    CHECK (writer_kind IN ('script', 'sql', 'registration', 'webhook', 'manual'));
