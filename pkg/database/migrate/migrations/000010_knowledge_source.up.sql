ALTER TABLE knowledge_insights
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'user';

CREATE INDEX IF NOT EXISTS idx_knowledge_insights_source ON knowledge_insights(source);
