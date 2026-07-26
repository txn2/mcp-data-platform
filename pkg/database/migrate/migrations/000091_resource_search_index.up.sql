-- 000091: managed-resource search index
--
-- Makes human-uploaded resources discoverable through the universal search
-- front door (#1012). Until now the only search over resources was an ILIKE on
-- display_name/description serving the portal list page, so an uploaded file was
-- unreachable from `search` and its contents were never matched at all.
--
-- A resource is a single embeddable unit, so the vector lives on the row itself,
-- mirroring portal_assets (000063), prompts (000062) and memory_records (000054)
-- rather than a dedicated vector table. SourceID for the indexjobs consumer
-- (pkg/resource/resourceindex) is the resource id; each unit yields one item.
-- Because the index IS the row, deleting a resource deletes its index entry.
--
--   1. content_text: the bounded text prefix extracted from the resource's blob
--      by the index consumer. Resource content lives in S3, not in Postgres, so
--      the lexical arm has nothing to match against unless the extracted text is
--      denormalized onto the row (the same shape portal_collections uses for
--      sections_text). Only text-family MIME types populate it; binary resources
--      keep the empty default and are indexed on metadata alone.
--
--   1b. content_indexed_at: when the consumer last settled the content question
--      for this row — extracted the text, or established that there is nothing to
--      extract (binary, oversized, no blob storage, object gone). NULL means the
--      content pass is still owed. It is a distinct gap signal from the embedding
--      because the two can diverge: a transient blob failure still produces a
--      valid metadata-only embedding, and without this column that success would
--      close the gap and the file's contents would never be indexed, while
--      coverage reported 100%.
--
--   2. embedding / embedding_model / embedding_text_hash: the vector plus the
--      provider-identity and content-hash breadcrumbs the shared indexjobs
--      framework (pkg/indexjobs) needs to dedup re-embeds by text hash and
--      detect model-swap gaps.
--
--   3. hnsw ANN index on embedding: matches the cosine `<=>` operator the ranked
--      search uses (vector_cosine_ops). Requires pgvector >= 0.5.0.
--
--   4. GIN full-text index: backs the lexical arm of hybrid ranking and the
--      lexical-only fallback used when no embedding provider is configured. It
--      is what finds a CSV column name that appears only inside the file — on a
--      deployment that runs the index queue, which requires an embedding
--      provider. Without one nothing fills content_text, so the same index
--      matches metadata only.
--
-- Existing rows get a NULL embedding, an empty content_text, and a NULL
-- content_indexed_at; the reconciler gap-detects them on either signal and
-- backfills both off the request path, so no data migration is needed here.
--
-- pgvector is enabled by migration 000031; re-enable defensively so this
-- migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS content_text        TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS content_indexed_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS embedding           vector(768),
    ADD COLUMN IF NOT EXISTS embedding_model     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding_text_hash BYTEA;

CREATE INDEX IF NOT EXISTS idx_resources_embedding_hnsw
    ON resources USING hnsw (embedding vector_cosine_ops);

-- resource_fts composes the lexical document from the same fields
-- resource.IndexText embeds (display name, description, category, filename,
-- tags, extracted content). tags is a TEXT[]; array_to_string flattens it so a
-- tag is matched without unnesting. It is wrapped in a function because a GIN
-- index expression requires every function in it be IMMUTABLE; the composition
-- is deterministic for these fixed text inputs, so marking the wrapper IMMUTABLE
-- is correct (the same argument portal_asset_fts in 000063 makes for its cast).
-- The request-path search must call this function with the same argument order
-- to hit the index.
CREATE OR REPLACE FUNCTION resource_fts(
    display_name text, description text, category text,
    filename text, tags text[], content_text text
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(display_name, '') || ' ' ||
        coalesce(description, '')  || ' ' ||
        coalesce(category, '')     || ' ' ||
        coalesce(filename, '')     || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        coalesce(content_text, ''));
$$;

CREATE INDEX IF NOT EXISTS idx_resources_search_fts
    ON resources USING gin (resource_fts(display_name, description, category, filename, tags, content_text));
