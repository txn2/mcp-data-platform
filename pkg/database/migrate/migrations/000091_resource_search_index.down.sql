-- Reverse 000091. Drop the resource search index, its FTS function, and the
-- search columns (including the denormalized content_text).
DROP INDEX IF EXISTS idx_resources_search_fts;
DROP FUNCTION IF EXISTS resource_fts(text, text, text, text, text[], text);
DROP INDEX IF EXISTS idx_resources_embedding_hnsw;

ALTER TABLE resources
    DROP COLUMN IF EXISTS embedding_text_hash,
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS embedding,
    DROP COLUMN IF EXISTS content_indexed_at,
    DROP COLUMN IF EXISTS content_text;
