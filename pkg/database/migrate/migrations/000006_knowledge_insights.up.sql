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
    status          TEXT NOT NULL DEFAULT 'pending'
);

CREATE INDEX IF NOT EXISTS idx_knowledge_insights_session_id ON knowledge_insights(session_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_insights_captured_by ON knowledge_insights(captured_by);
CREATE INDEX IF NOT EXISTS idx_knowledge_insights_status ON knowledge_insights(status);
CREATE INDEX IF NOT EXISTS idx_knowledge_insights_category ON knowledge_insights(category);
CREATE INDEX IF NOT EXISTS idx_knowledge_insights_created_at ON knowledge_insights(created_at);
