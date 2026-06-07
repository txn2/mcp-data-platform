-- Reverse 000062. Drop the prompt embedding indexes and columns.
DROP INDEX IF EXISTS idx_prompts_search_fts;
DROP INDEX IF EXISTS idx_prompts_embedding_hnsw;

ALTER TABLE prompts
    DROP COLUMN IF EXISTS embedding_text_hash,
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS embedding;
