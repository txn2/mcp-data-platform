DROP INDEX IF EXISTS idx_portal_knowledge_pages_builtin_deleted;
ALTER TABLE portal_knowledge_pages DROP COLUMN IF EXISTS builtin;
