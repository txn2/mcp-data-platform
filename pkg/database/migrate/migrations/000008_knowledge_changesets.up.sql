CREATE TABLE IF NOT EXISTS knowledge_changesets (
    id                  TEXT PRIMARY KEY,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    target_urn          TEXT NOT NULL,
    change_type         TEXT NOT NULL,
    previous_value      JSONB NOT NULL DEFAULT '{}',
    new_value           JSONB NOT NULL DEFAULT '{}',
    source_insight_ids  JSONB NOT NULL DEFAULT '[]',
    approved_by         TEXT NOT NULL DEFAULT '',
    applied_by          TEXT NOT NULL DEFAULT '',
    rolled_back         BOOLEAN NOT NULL DEFAULT FALSE,
    rolled_back_by      TEXT NOT NULL DEFAULT '',
    rolled_back_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_knowledge_changesets_target_urn ON knowledge_changesets(target_urn);
CREATE INDEX IF NOT EXISTS idx_knowledge_changesets_applied_by ON knowledge_changesets(applied_by);
CREATE INDEX IF NOT EXISTS idx_knowledge_changesets_rolled_back ON knowledge_changesets(rolled_back);
CREATE INDEX IF NOT EXISTS idx_knowledge_changesets_created_at ON knowledge_changesets(created_at);

-- Add apply tracking columns to knowledge_insights
ALTER TABLE knowledge_insights
    ADD COLUMN IF NOT EXISTS applied_by     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS applied_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS changeset_ref  TEXT NOT NULL DEFAULT '';
