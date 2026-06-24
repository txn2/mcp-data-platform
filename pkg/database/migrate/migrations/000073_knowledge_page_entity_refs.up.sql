-- 000073: knowledge_page_entity_refs (#664 Phase 0)
--
-- A knowledge page references 0..N entities it provides knowledge about. Internal
-- entities are real foreign keys (referential integrity and cascade cleanup);
-- only the external DataHub case is a text URN, because there is no local row to
-- reference. This mirrors the portal_threads / portal_shares 1-of-N target
-- polymorphism: exactly one target is set per row, enforced by a CHECK.
--
-- Phase 0 populates the DataHub (entity_urn) references that apply_knowledge was
-- silently dropping when it promoted a business_knowledge insight to a page. The
-- foreign-key target columns are part of the same designed table and are written
-- by later phases (the authoring picker and inline body references); the source
-- column records each reference's origin so the body-scan reconciliation can
-- touch only inline references without clobbering picked or promoted ones.
CREATE TABLE IF NOT EXISTS knowledge_page_entity_refs (
    id              TEXT PRIMARY KEY,
    page_id         TEXT NOT NULL REFERENCES portal_knowledge_pages(id) ON DELETE CASCADE,
    target_type     TEXT NOT NULL,                       -- asset|prompt|collection|knowledge_page|connection|datahub
    asset_id        TEXT REFERENCES portal_assets(id) ON DELETE CASCADE,
    prompt_id       UUID REFERENCES prompts(id) ON DELETE CASCADE,
    collection_id   TEXT REFERENCES portal_collections(id) ON DELETE CASCADE,
    ref_page_id     TEXT REFERENCES portal_knowledge_pages(id) ON DELETE CASCADE,
    connection_kind TEXT,
    connection_name TEXT,
    entity_urn      TEXT,                                -- DataHub or other external; no FK
    source          TEXT NOT NULL DEFAULT 'promoted',    -- promoted|manual|inline
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (connection_kind, connection_name)
        REFERENCES connection_instances(kind, name) ON DELETE CASCADE,
    CONSTRAINT chk_kp_entity_ref_target CHECK (
        (target_type = 'asset'          AND asset_id IS NOT NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL) OR
        (target_type = 'prompt'         AND prompt_id IS NOT NULL AND asset_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL) OR
        (target_type = 'collection'     AND collection_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL) OR
        (target_type = 'knowledge_page' AND ref_page_id IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL AND entity_urn IS NULL) OR
        (target_type = 'connection'     AND connection_kind IS NOT NULL AND connection_name IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND entity_urn IS NULL) OR
        (target_type = 'datahub'        AND entity_urn IS NOT NULL AND asset_id IS NULL AND prompt_id IS NULL AND collection_id IS NULL AND ref_page_id IS NULL AND connection_kind IS NULL AND connection_name IS NULL)
    ),
    CONSTRAINT chk_kp_entity_ref_source CHECK (source IN ('promoted', 'manual', 'inline'))
);

-- Forward lookup: all references of a page (ListEntityRefs).
CREATE INDEX IF NOT EXISTS idx_kp_entity_refs_page ON knowledge_page_entity_refs(page_id);

-- Per-target-type uniqueness so a reference is recorded once per page (union on
-- promotion). One partial unique index per target, since exactly one column is set.
CREATE UNIQUE INDEX IF NOT EXISTS uq_kp_entity_refs_asset ON knowledge_page_entity_refs(page_id, asset_id) WHERE asset_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_kp_entity_refs_prompt ON knowledge_page_entity_refs(page_id, prompt_id) WHERE prompt_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_kp_entity_refs_collection ON knowledge_page_entity_refs(page_id, collection_id) WHERE collection_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_kp_entity_refs_page_ref ON knowledge_page_entity_refs(page_id, ref_page_id) WHERE ref_page_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_kp_entity_refs_connection ON knowledge_page_entity_refs(page_id, connection_kind, connection_name) WHERE connection_kind IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_kp_entity_refs_urn ON knowledge_page_entity_refs(page_id, entity_urn) WHERE entity_urn IS NOT NULL;
