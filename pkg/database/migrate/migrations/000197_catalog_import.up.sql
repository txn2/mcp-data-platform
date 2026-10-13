-- The one-time import of a deployment's DataHub into the platform catalog and
-- the rewrite of the urn:li: references platform tables hold (#1923).

-- Lineage edges the import carried over from DataHub are recorded as the
-- import's, so they stay told apart from the edges the platform records itself.
ALTER TABLE catalog_lineage DROP CONSTRAINT IF EXISTS catalog_lineage_writer_kind_check;
ALTER TABLE catalog_lineage ADD CONSTRAINT catalog_lineage_writer_kind_check
    CHECK (writer_kind IN ('script', 'sql', 'registration', 'webhook', 'manual', 'import'));

-- Each run of the import: who started it, against which DataHub, and what it
-- reported. A run with no finished_at is under way, or was cut off by its
-- replica stopping, which the advisory lock the run holds tells apart.
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

-- What each DataHub URN the import read became in the catalog. The rewrite of
-- stored references reads it, and a second run of the import keeps the
-- definitions the first one created rather than creating them again.
CREATE TABLE IF NOT EXISTS catalog_import_map (
    urn         TEXT        PRIMARY KEY,
    reference   TEXT        NOT NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A knowledge page cites a catalog entity by its mcp: reference. A citation
-- of a DataHub entity keeps target_type 'datahub' until the rewrite turns it
-- into the catalog reference it names.
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
