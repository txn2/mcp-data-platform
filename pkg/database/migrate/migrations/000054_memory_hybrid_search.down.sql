-- Reverse 000054: drop the hybrid-search indexes and embedding
-- breadcrumb columns. The extension is left in place (other tables
-- depend on it).

DROP INDEX IF EXISTS idx_memory_records_content_fts;
DROP INDEX IF EXISTS idx_memory_records_embedding_hnsw;

ALTER TABLE memory_records
    DROP COLUMN IF EXISTS embedding_text_hash,
    DROP COLUMN IF EXISTS embedding_model;
