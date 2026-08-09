-- Reverse 000097. Restore the single-vector columns and their ANN index, and
-- drop the chunk table. The restored embedding column comes back NULL and
-- embedding_model is reset to '', so every live page is a gap the reconciler
-- re-embeds under the pre-chunking code; no vector is carried across the
-- boundary because a page-level vector cannot be reconstructed from chunks.
ALTER TABLE portal_knowledge_pages ADD COLUMN IF NOT EXISTS embedding vector(768);
ALTER TABLE portal_knowledge_pages ADD COLUMN IF NOT EXISTS embedding_text_hash BYTEA;

CREATE INDEX IF NOT EXISTS idx_portal_knowledge_pages_embedding_hnsw
    ON portal_knowledge_pages USING hnsw (embedding vector_cosine_ops);

UPDATE portal_knowledge_pages SET embedding_model = '';

DROP INDEX IF EXISTS idx_portal_knowledge_page_chunks_embedding_hnsw;
DROP TABLE IF EXISTS portal_knowledge_page_embedding_chunks;
