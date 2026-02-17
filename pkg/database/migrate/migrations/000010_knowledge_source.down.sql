DROP INDEX IF EXISTS idx_knowledge_insights_source;

ALTER TABLE knowledge_insights DROP COLUMN IF EXISTS source;
