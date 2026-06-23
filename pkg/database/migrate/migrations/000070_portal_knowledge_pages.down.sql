DROP INDEX IF EXISTS idx_portal_knowledge_pages_live;
DROP INDEX IF EXISTS idx_portal_knowledge_pages_search_fts;
DROP FUNCTION IF EXISTS portal_knowledge_page_fts(text, text, jsonb);
DROP INDEX IF EXISTS idx_portal_knowledge_pages_embedding_hnsw;
DROP INDEX IF EXISTS idx_portal_knowledge_pages_slug;
DROP TABLE IF EXISTS portal_knowledge_pages;
