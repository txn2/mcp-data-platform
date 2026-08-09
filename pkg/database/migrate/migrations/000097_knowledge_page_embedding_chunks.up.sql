-- 000097: chunked embeddings for knowledge pages (#1242)
--
-- A knowledge page used to carry exactly one vector on its own row (000070).
-- The embedding provider caps each input (embedding.DefaultMaxInputBytes, ~6 KB
-- against nomic-embed-text's 2048-token context), so every byte of a page past
-- that cap was trimmed off before the vector was computed: the tail of a large
-- page was invisible to the semantic arm of search and to the create-time
-- near-duplicate gate, while remaining visible to the lexical arm. The oversize
-- split suggestion (#705) does not fire until 16 KiB, so a page between the cap
-- and that threshold sat in a band where semantic coverage was silently partial.
--
-- The page is now embedded as a SET of chunks, each sized under the provider's
-- input budget, so the whole page reaches the model. Chunks live in a child
-- table because pgvector stores one vector per column and cannot ANN-index an
-- array of vectors — N chunks physically require N rows. The consistency the
-- inline column provided (same database, same transaction, nothing external to
-- drift) is preserved: the chunk set is replaced in one transaction, and the
-- request path deletes a page's chunks in the same transaction that changes its
-- indexed text, so an edit never leaves a stale vector behind.
--
-- Search stays page-granular: both vector readers (the hybrid search arm and the
-- dedup probe) rank chunks and keep the best-scoring chunk per page.
--
-- portal_knowledge_pages.embedding_model survives as the SET-level index-state
-- marker: the reconciler's gap query reads exactly one row per page to decide
-- whether the page's chunk set was produced by the current provider model. The
-- per-chunk model/text-hash columns serve the framework's per-item dedup, which
-- is what lets an edit to one section re-embed only the chunks that changed.
--
-- The single-vector columns are dropped outright (no dual-read path). Resetting
-- embedding_model to '' makes every live page a gap on the reconciler's next
-- sweep, which is the same backfill machinery that populated the column when
-- page embeddings were introduced.
--
-- pgvector is enabled by migration 000031; re-enable defensively so this
-- migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS portal_knowledge_page_embedding_chunks (
    page_id     TEXT        NOT NULL REFERENCES portal_knowledge_pages(id) ON DELETE CASCADE,
    chunk_index INTEGER     NOT NULL,
    text_hash   BYTEA       NOT NULL,
    embedding   vector(768) NOT NULL,
    model       TEXT        NOT NULL DEFAULT '',
    dim         INTEGER     NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (page_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_portal_knowledge_page_chunks_embedding_hnsw
    ON portal_knowledge_page_embedding_chunks USING hnsw (embedding vector_cosine_ops);

-- Pages soft-delete (deleted_at), so the FK cascade only covers a hard delete.
-- Every read joins back to the page and filters deleted_at IS NULL, exactly as
-- the entity-reference reads do.
DROP INDEX IF EXISTS idx_portal_knowledge_pages_embedding_hnsw;

ALTER TABLE portal_knowledge_pages DROP COLUMN IF EXISTS embedding;
ALTER TABLE portal_knowledge_pages DROP COLUMN IF EXISTS embedding_text_hash;

UPDATE portal_knowledge_pages SET embedding_model = '';
