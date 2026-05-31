-- 000054: memory hybrid search
--
-- Hybrid (vector + lexical) recall for memory_records (#507). Three
-- additions, all on the existing memory_records table:
--
--   1. embedding_model / embedding_text_hash: the provider-identity and
--      content-hash breadcrumbs the shared indexjobs framework
--      (pkg/indexjobs) needs to (a) dedup re-embeds by text hash and
--      (b) detect model-swap gaps. memory registers as an indexjobs
--      consumer (source_kind = 'memory') so a memory saved while the
--      embedder was down, or left stale by a model swap, self-heals off
--      the request path. The synchronous embed-on-write path stamps
--      both columns when the provider is healthy, so a healthy row never
--      looks like a gap. Dim is not stored: it is derivable from the
--      stored vector's length.
--
--   2. hnsw ANN index on embedding: the cosine recall query
--      (ORDER BY embedding <=> $1) was an O(n) sequential scan with no
--      index. hnsw matches the cosine `<=>` operator (vector_cosine_ops),
--      needs no training step, and builds on an empty or populated table.
--      Requires pgvector >= 0.5.0, which the platform's pgvector image
--      ships (dev: pgvector/pgvector:pg16).
--
--   3. GIN full-text index on to_tsvector('english', content): backs the
--      new lexical retrieval arm. Lexical queries MUST use this exact
--      expression to hit the index. Lexical also surfaces NULL-embedding
--      rows that the vector arm skips, which is what makes recall degrade
--      gracefully to keyword matching during an embedder outage.
--
-- pgvector is enabled by migration 000031; re-enable defensively so this
-- migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE memory_records
    ADD COLUMN IF NOT EXISTS embedding_model     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding_text_hash BYTEA;

CREATE INDEX IF NOT EXISTS idx_memory_records_embedding_hnsw
    ON memory_records USING hnsw (embedding vector_cosine_ops);

CREATE INDEX IF NOT EXISTS idx_memory_records_content_fts
    ON memory_records USING gin (to_tsvector('english', content));
