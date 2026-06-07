-- 000063: portal asset + collection embeddings
--
-- Inline embedding columns on portal_assets and portal_collections backing
-- semantic + lexical discovery over saved assets and curated collections (#550).
-- An asset and a collection are each a single embeddable unit, so the vector
-- lives on the row itself, mirroring prompts (000062) and memory_records
-- (000054) rather than a dedicated vector table. SourceID for the indexjobs
-- consumers is the asset id / collection id; each unit yields exactly one item.
--
-- The reconciler gap-detects non-deleted rows whose embedding is missing or was
-- produced by a different model. The request-path Update/SetSections clears
-- these columns whenever the indexed text changes, so the reconciler re-embeds
-- off the request path and an edit never leaves a stale vector behind.
--
--   1. embedding / embedding_model / embedding_text_hash: the vector plus the
--      provider-identity and content-hash breadcrumbs the shared indexjobs
--      framework (pkg/indexjobs) needs to dedup re-embeds by text hash and
--      detect model-swap gaps. Dim is derivable from the stored vector length.
--
--   2. hnsw ANN index on embedding: matches the cosine `<=>` operator the ranked
--      search uses (vector_cosine_ops). Requires pgvector >= 0.5.0.
--
--   3. GIN full-text index: backs the lexical arm of hybrid ranking and the
--      lexical-only fallback used when no embedding provider is configured.
--
-- Collections additionally denormalize their section titles + descriptions into
-- sections_text so the lexical arm can match section content (which lives in a
-- separate table) without a join in the index expression. The request-path
-- SetSections maintains it; this migration backfills it for existing rows.
--
-- pgvector is enabled by migration 000031; re-enable defensively so this
-- migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

-- --- Assets ---------------------------------------------------------------

ALTER TABLE portal_assets
    ADD COLUMN IF NOT EXISTS embedding           vector(768),
    ADD COLUMN IF NOT EXISTS embedding_model     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding_text_hash BYTEA;

CREATE INDEX IF NOT EXISTS idx_portal_assets_embedding_hnsw
    ON portal_assets USING hnsw (embedding vector_cosine_ops);

-- portal_asset_fts composes the lexical document from the same fields
-- portal.AssetIndexText embeds (name + description + tags). tags is a JSONB
-- array; tags::text yields the bracketed/quoted JSON whose word tokens
-- to_tsvector extracts (the punctuation is dropped), so a tag is findable
-- without unnesting the array. It is wrapped in a function because the cast is
-- composed with concatenation, and a GIN index expression requires every
-- function be IMMUTABLE; the composition is deterministic, so marking the
-- wrapper IMMUTABLE is correct. The request-path search must call this function
-- with the same argument order to hit the index.
CREATE OR REPLACE FUNCTION portal_asset_fts(
    name text, description text, tags jsonb
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(name, '')        || ' ' ||
        coalesce(description, '')  || ' ' ||
        coalesce(tags::text, ''));
$$;

CREATE INDEX IF NOT EXISTS idx_portal_assets_search_fts
    ON portal_assets USING gin (portal_asset_fts(name, description, tags));

-- --- Collections ----------------------------------------------------------

ALTER TABLE portal_collections
    ADD COLUMN IF NOT EXISTS sections_text       TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding           vector(768),
    ADD COLUMN IF NOT EXISTS embedding_model     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding_text_hash BYTEA;

-- Backfill sections_text from the existing sections so the lexical arm covers
-- collections created before this migration; their embeddings stay NULL and the
-- reconciler backfills them off the request path.
UPDATE portal_collections pc
   SET sections_text = COALESCE((
        SELECT string_agg(
                   coalesce(cs.title, '') || ' ' || coalesce(cs.description, ''), ' '
                   ORDER BY cs.position)
          FROM portal_collection_sections cs
         WHERE cs.collection_id = pc.id), '');

CREATE INDEX IF NOT EXISTS idx_portal_collections_embedding_hnsw
    ON portal_collections USING hnsw (embedding vector_cosine_ops);

-- portal_collection_fts composes the lexical document from the same fields
-- portal.CollectionIndexText embeds (name + description + sections_text, the
-- denormalized section titles + descriptions). IMMUTABLE for the same GIN-index
-- reason as portal_asset_fts.
CREATE OR REPLACE FUNCTION portal_collection_fts(
    name text, description text, sections_text text
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(name, '')          || ' ' ||
        coalesce(description, '')    || ' ' ||
        coalesce(sections_text, ''));
$$;

CREATE INDEX IF NOT EXISTS idx_portal_collections_search_fts
    ON portal_collections USING gin (portal_collection_fts(name, description, sections_text));
