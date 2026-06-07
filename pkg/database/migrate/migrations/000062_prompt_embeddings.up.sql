-- 000062: prompt embeddings
--
-- Inline embedding columns on the prompts table backing semantic discovery for
-- the prompt library (#557, epic #525 phase 4). A prompt is a single embeddable
-- unit (title + description + body + tags), so its vector lives on the row
-- itself, mirroring memory_records (000054) rather than a dedicated vector
-- table. SourceID for the indexjobs prompts consumer is the prompt id; each
-- unit yields exactly one item.
--
-- Only approved prompts are indexed: the prompts consumer (source_kind =
-- 'prompts') gap-detects approved + enabled rows whose embedding is missing or
-- was produced by a different model. The request-path Update clears these
-- columns whenever the indexed text changes, so the reconciler re-embeds off
-- the request path and a content edit never leaves a stale vector behind.
--
--   1. embedding / embedding_model / embedding_text_hash: the vector plus the
--      provider-identity and content-hash breadcrumbs the shared indexjobs
--      framework (pkg/indexjobs) needs to dedup re-embeds by text hash and
--      detect model-swap gaps. Dim is not stored: it is derivable from the
--      stored vector's length.
--
--   2. hnsw ANN index on embedding: matches the cosine `<=>` operator the
--      ranked search uses (vector_cosine_ops). Requires pgvector >= 0.5.0,
--      which the platform's pgvector image ships.
--
--   3. GIN full-text index: backs the lexical arm of hybrid ranking and the
--      lexical-only fallback used when no embedding provider is configured.
--      Lexical queries MUST use this exact expression to hit the index.
--
-- pgvector is enabled by migration 000031; re-enable defensively so this
-- migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE prompts
    ADD COLUMN IF NOT EXISTS embedding           vector(768),
    ADD COLUMN IF NOT EXISTS embedding_model     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding_text_hash BYTEA;

CREATE INDEX IF NOT EXISTS idx_prompts_embedding_hnsw
    ON prompts USING hnsw (embedding vector_cosine_ops);

-- prompt_fts composes the lexical document from the same fields prompt.IndexText
-- embeds (title + description + body + tags), where the title is
-- coalesce(nullif(display_name,''), name) so a prompt with no display_name is
-- still findable by name. It is wrapped in a function because array_to_string is
-- only STABLE, and a GIN index expression requires every function be IMMUTABLE;
-- the composition is deterministic for a text[] with a constant delimiter, so
-- marking the wrapper IMMUTABLE is correct. The request-path search must call
-- prompt_fts with the same argument order to hit this index.
CREATE OR REPLACE FUNCTION prompt_fts(
    display_name text, name text, description text, content text, tags text[]
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(nullif(display_name, ''), name) || ' ' ||
        coalesce(description, '')  || ' ' ||
        coalesce(content, '')      || ' ' ||
        coalesce(array_to_string(tags, ' '), ''));
$$;

CREATE INDEX IF NOT EXISTS idx_prompts_search_fts
    ON prompts USING gin (prompt_fts(display_name, name, description, content, tags));
