-- Generic thread substrate (#601, epic #600). A thread is a tracked work item
-- with a target, a lifecycle, and a typed event timeline. Feedback is the first
-- kind; later kinds (internal task, data-quality flag) slot in with no schema
-- churn. Mirrors the portal_shares 1-of-N target polymorphism (000061):
-- exactly one of asset_id/collection_id/prompt_id is set for those target
-- types, all null for standalone. prompt_id is UUID (prompts.id, see 000030);
-- asset_id/collection_id are TEXT like portal_assets/portal_collections.
CREATE TABLE IF NOT EXISTS portal_threads (
    id                  TEXT PRIMARY KEY,
    kind                TEXT NOT NULL,                       -- comment|question|correction|rating|approval|rejection|suggestion
    target_type         TEXT NOT NULL,                       -- asset|collection|prompt|standalone
    asset_id            TEXT REFERENCES portal_assets(id),
    collection_id       TEXT REFERENCES portal_collections(id),
    prompt_id           UUID REFERENCES prompts(id) ON DELETE CASCADE,
    anchor              JSONB,                               -- null = object-level; non-null = inline-anchored selection
    target_version      INTEGER,
    title               TEXT NOT NULL DEFAULT '',
    author_id           TEXT NOT NULL,
    author_email        TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'open',        -- open|answered|resolved|wont_fix|acknowledged
    requires_resolution BOOLEAN NOT NULL DEFAULT FALSE,
    validation_state    TEXT NOT NULL DEFAULT 'none',        -- none|pending|validated|disputed (Phase 3)
    insight_id          TEXT,                                -- Phase 2 bridge to memory_records, nullable
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ,
    CONSTRAINT chk_threads_target CHECK (
        (target_type = 'standalone' AND asset_id IS NULL AND collection_id IS NULL AND prompt_id IS NULL) OR
        (target_type = 'asset'      AND asset_id IS NOT NULL AND collection_id IS NULL AND prompt_id IS NULL) OR
        (target_type = 'collection' AND asset_id IS NULL AND collection_id IS NOT NULL AND prompt_id IS NULL) OR
        (target_type = 'prompt'     AND asset_id IS NULL AND collection_id IS NULL AND prompt_id IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_portal_threads_asset_id ON portal_threads(asset_id)
    WHERE asset_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_portal_threads_collection_id ON portal_threads(collection_id)
    WHERE collection_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_portal_threads_prompt_id ON portal_threads(prompt_id)
    WHERE prompt_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_portal_threads_standalone ON portal_threads(created_at)
    WHERE target_type = 'standalone' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_portal_threads_author_id ON portal_threads(author_id);
