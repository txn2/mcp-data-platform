-- Recreate knowledge_insights from memory_records.
CREATE TABLE IF NOT EXISTS knowledge_insights (
    id              TEXT PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    session_id      TEXT NOT NULL DEFAULT '',
    captured_by     TEXT NOT NULL DEFAULT '',
    persona         TEXT NOT NULL DEFAULT '',
    category        TEXT NOT NULL,
    insight_text    TEXT NOT NULL,
    confidence      TEXT NOT NULL DEFAULT 'medium',
    entity_urns     JSONB NOT NULL DEFAULT '[]',
    related_columns JSONB NOT NULL DEFAULT '[]',
    suggested_actions JSONB NOT NULL DEFAULT '[]',
    status          TEXT NOT NULL DEFAULT 'pending',
    source          TEXT NOT NULL DEFAULT 'user',
    reviewed_by     TEXT NOT NULL DEFAULT '',
    reviewed_at     TIMESTAMPTZ,
    review_notes    TEXT NOT NULL DEFAULT '',
    applied_by      TEXT NOT NULL DEFAULT '',
    applied_at      TIMESTAMPTZ,
    changeset_ref   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_knowledge_insights_session_id ON knowledge_insights(session_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_insights_captured_by ON knowledge_insights(captured_by);
CREATE INDEX IF NOT EXISTS idx_knowledge_insights_status ON knowledge_insights(status);
CREATE INDEX IF NOT EXISTS idx_knowledge_insights_category ON knowledge_insights(category);
CREATE INDEX IF NOT EXISTS idx_knowledge_insights_created_at ON knowledge_insights(created_at);
CREATE INDEX IF NOT EXISTS idx_knowledge_insights_source ON knowledge_insights(source);

-- Migrate data back from memory_records to knowledge_insights.
-- NOTE: This migration converts ALL memory dimensions (knowledge, event, entity,
-- relationship, preference) into knowledge_insights rows. Non-knowledge records
-- are mapped with category='general' since knowledge_insights has no dimension
-- column. Embedding data, staleness tracking fields, and dimension-specific
-- semantics are lost during this rollback.
INSERT INTO knowledge_insights (
    id, created_at, session_id, captured_by, persona, category,
    insight_text, confidence, entity_urns, related_columns,
    suggested_actions, status, source, reviewed_by, review_notes,
    applied_by, changeset_ref
)
SELECT
    id,
    created_at,
    COALESCE(metadata->>'session_id', ''),
    created_by,
    persona,
    CASE
        WHEN dimension = 'knowledge' THEN category
        ELSE 'general'
    END,
    content,
    confidence,
    entity_urns,
    related_columns,
    COALESCE(metadata->'suggested_actions', '[]'::jsonb),
    CASE
        WHEN status = 'superseded' THEN 'superseded'
        WHEN status = 'archived' THEN 'rejected'
        WHEN status = 'stale' THEN 'pending'
        ELSE COALESCE(metadata->>'legacy_status', 'pending')
    END,
    source,
    COALESCE(metadata->>'reviewed_by', ''),
    COALESCE(metadata->>'review_notes', ''),
    COALESCE(metadata->>'applied_by', ''),
    COALESCE(metadata->>'changeset_ref', '')
FROM memory_records;

-- Drop memory_records table.
DROP TABLE IF EXISTS memory_records;
