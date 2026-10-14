-- Restores the shapes 000197 created. The rows the up migration dropped are
-- not restored.
ALTER TABLE knowledge_page_entity_refs DROP CONSTRAINT IF EXISTS chk_kp_entity_ref_target;
ALTER TABLE knowledge_page_entity_refs ADD CONSTRAINT chk_kp_entity_ref_target CHECK (
    (target_type = 'asset'          AND asset_id IS NOT NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'prompt'         AND prompt_id IS NOT NULL AND asset_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'collection'     AND collection_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'knowledge_page' AND ref_page_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type = 'connection'     AND connection_kind IS NOT NULL AND connection_name IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND entity_urn IS NULL AND script_id IS NULL) OR
    (target_type IN ('datahub', 'catalog') AND entity_urn IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND script_id IS NULL) OR
    (target_type = 'script'         AND script_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL)
);

CREATE TABLE IF NOT EXISTS catalog_imports (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source_url  TEXT        NOT NULL,
    started_by  TEXT        NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    report      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    error       TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_catalog_imports_started ON catalog_imports (started_at DESC);

CREATE TABLE IF NOT EXISTS catalog_import_map (
    urn         TEXT        PRIMARY KEY,
    reference   TEXT        NOT NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
