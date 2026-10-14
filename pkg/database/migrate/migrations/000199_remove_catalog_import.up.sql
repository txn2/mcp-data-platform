-- The platform no longer reads DataHub (#1925). The one-time import (#1923)
-- is gone with it, and so are its run log and its map of what each URN it
-- read became. Lineage edges the import recorded stay: they are the
-- deployment's lineage, and writer_kind 'import' still says where they came
-- from.
DROP TABLE IF EXISTS catalog_import_map;
DROP TABLE IF EXISTS catalog_imports;

-- A DataHub connection the platform stored (one backfilled from the file
-- configuration, or one an operator added) names a kind no toolkit serves any
-- more, so it would be listed as a connection nothing can call. Its citations,
-- tokens and alerts go with it.
DELETE FROM connection_instances WHERE kind = 'datahub';
DELETE FROM connection_oauth_tokens WHERE connection_kind = 'datahub';
DELETE FROM connection_auth_alerts WHERE connection_kind = 'datahub';

-- A knowledge page citation of a urn:li: entity names nothing the platform can
-- resolve. The import rewrote every one it could map into a catalog
-- reference; what is left is dropped with the target type that described it.
-- The page body keeps the text it was written with.
DELETE FROM knowledge_page_entity_refs WHERE target_type = 'datahub';
ALTER TABLE knowledge_page_entity_refs DROP CONSTRAINT IF EXISTS chk_kp_entity_ref_target;
ALTER TABLE knowledge_page_entity_refs ADD CONSTRAINT chk_kp_entity_ref_target CHECK (
    (target_type = 'asset'          AND asset_id IS NOT NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'prompt'         AND prompt_id IS NOT NULL AND asset_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'collection'     AND collection_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'knowledge_page' AND ref_page_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'connection'     AND connection_kind IS NOT NULL AND connection_name IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'catalog'        AND entity_urn IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND script_id IS NULL) OR
    (target_type = 'script'         AND script_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL)
);
