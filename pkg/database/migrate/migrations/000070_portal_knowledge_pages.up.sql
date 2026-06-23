-- 000070: portal knowledge pages (business-ontology home, #633/#634 redesign)
--
-- Knowledge pages are the platform's CANONICAL store for general business and
-- domain knowledge, the internal-knowledge sibling of DataHub. They are a
-- DISTINCT, org-shared entity, NOT portal_assets: assets are owner-scoped
-- personal artifacts, whereas a knowledge page is shared-readable by everyone
-- and edited by personas with apply_knowledge access. The provisional "draft" of
-- knowledge is the memory/insight inbox; a page, once it exists, is canonical,
-- so there is no draft/published lifecycle here, only a soft-delete.
--
-- Unlike assets (whose body lives in S3), a page's markdown body is stored
-- INLINE so it is directly embeddable and full-text searchable: the asset
-- indexer embeds only name/description/tags and never reads the S3 body, which
-- is exactly the gap this entity must not inherit (page CONTENT must be
-- searchable). The vector lives on the row, one embedding per page, mirroring
-- portal_assets/portal_collections (000063), prompts (000062), and
-- memory_records (000054) rather than a dedicated vector table.
--
-- The reconciler (pkg/indexjobs) gap-detects non-deleted rows whose embedding is
-- missing or was produced by a different model; the request-path Create/Update
-- clears the embedding columns whenever the indexed text (title/body/tags)
-- changes, so the reconciler re-embeds off the request path and an edit never
-- leaves a stale vector behind.
--
-- pgvector is enabled by migration 000031; re-enable defensively so this
-- migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS portal_knowledge_pages (
    id                  TEXT PRIMARY KEY,
    slug                TEXT,
    title               TEXT NOT NULL,
    summary             TEXT NOT NULL DEFAULT '',
    body                TEXT NOT NULL DEFAULT '',
    tags                JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by          TEXT NOT NULL DEFAULT '',
    created_email       TEXT NOT NULL DEFAULT '',
    updated_by          TEXT NOT NULL DEFAULT '',
    current_version     INT  NOT NULL DEFAULT 1,
    embedding           vector(768),
    embedding_model     TEXT NOT NULL DEFAULT '',
    embedding_text_hash BYTEA,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ
);

-- A slug is a stable, human-friendly key for topical consolidation ("the one
-- living Seasons page"): apply_knowledge promotion finds-or-creates by slug so a
-- business fact updates the existing page instead of appending a new one. Unique
-- among live pages only, so a removed page's slug can be reused.
CREATE UNIQUE INDEX IF NOT EXISTS idx_portal_knowledge_pages_slug
    ON portal_knowledge_pages (slug)
    WHERE slug IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_portal_knowledge_pages_embedding_hnsw
    ON portal_knowledge_pages USING hnsw (embedding vector_cosine_ops);

-- portal_knowledge_page_fts composes the lexical document from the same fields
-- portal.KnowledgePageIndexText embeds (title + body + tags). tags is a JSONB
-- array; tags::text yields the bracketed JSON whose word tokens to_tsvector
-- extracts. IMMUTABLE so it can back a GIN index expression (same rationale as
-- portal_asset_fts, 000063); the request-path search must call it with the same
-- argument order to hit the index.
CREATE OR REPLACE FUNCTION portal_knowledge_page_fts(
    title text, body text, tags jsonb
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(title, '') || ' ' ||
        coalesce(body, '')  || ' ' ||
        coalesce(tags::text, ''));
$$;

CREATE INDEX IF NOT EXISTS idx_portal_knowledge_pages_search_fts
    ON portal_knowledge_pages USING gin (portal_knowledge_page_fts(title, body, tags));

CREATE INDEX IF NOT EXISTS idx_portal_knowledge_pages_live
    ON portal_knowledge_pages (updated_at DESC)
    WHERE deleted_at IS NULL;
