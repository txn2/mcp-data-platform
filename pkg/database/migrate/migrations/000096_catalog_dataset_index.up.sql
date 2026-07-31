-- 000096: platform-side semantic index over catalog dataset descriptions
--
-- Gives a DataHub-resident fact a search-time route that does not depend on a
-- tool result already naming the entity it hangs off (#1131). Until now the
-- catalog's only text route through `search` was DataHub's own keyword search:
-- a description written by apply_knowledge was found when a query happened to
-- share its words, and only after DataHub's own index caught up. Knowledge-page
-- sinks had no such gap because they ride the platform's embedding search, so
-- discoverability depended on which sink an operator picked.
--
-- catalog_datasets is the platform's own copy of the catalog's dataset text
-- (name, description, tags, domain) plus its vector. The rows are materialized
-- by the indexjobs consumer in internal/platform/datasetindex: its Source
-- enumerates the catalog, upserts the rows here, and the framework embeds them;
-- the Sink's atomic replace drops rows for datasets the catalog no longer
-- returns. The catalog remains the system of record — nothing writes back to it
-- and every hit is dereferenced against DataHub by URN — so this table is a
-- discardable index, not a second source of truth.
--
--   1. urn is the primary key: it is the item id the indexjobs framework keys
--      vectors on, the reference `fetch` dereferences, and the identity the
--      connection boundary (#1108) is evaluated against.
--
--   2. embedding / embedding_model / embedding_text_hash: the vector plus the
--      provider-identity and content-hash breadcrumbs the shared indexjobs
--      framework (pkg/indexjobs) needs to dedup re-embeds by text hash and
--      detect model-swap gaps. A freshly synced row starts with a NULL
--      embedding and is picked up by the reconciler's gap sweep; the lexical
--      arm can already match it in the meantime.
--
--   3. hnsw ANN index on embedding: matches the cosine `<=>` operator the ranked
--      search uses (vector_cosine_ops). Requires pgvector >= 0.5.0.
--
--   4. GIN full-text index: backs the lexical arm of hybrid ranking and the
--      lexical-only ranking used when no embedding provider is configured.
--
-- catalog_dataset_sync holds one row for the whole deployment recording when
-- the catalog was last enumerated. The sweep interval is a property of the
-- catalog, not of any one row, and an empty catalog has no row to carry it —
-- so it lives here rather than being inferred from a MAX() over the entries.
-- The primary key is the constant TRUE, which is what makes "one row" a
-- constraint the database enforces rather than a convention the code
-- remembers.
--
-- pgvector is enabled by migration 000031; re-enable defensively so this
-- migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS catalog_datasets (
    urn                 TEXT        PRIMARY KEY,
    name                TEXT        NOT NULL DEFAULT '',
    description         TEXT        NOT NULL DEFAULT '',
    tags                TEXT[]      NOT NULL DEFAULT '{}',
    domain              TEXT        NOT NULL DEFAULT '',
    embedding           vector(768),
    embedding_model     TEXT        NOT NULL DEFAULT '',
    embedding_text_hash BYTEA,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_catalog_datasets_embedding_hnsw
    ON catalog_datasets USING hnsw (embedding vector_cosine_ops);

-- catalog_dataset_fts composes the lexical document from the same fields
-- datasetindex.IndexText embeds (name, description, tags, domain). tags is a
-- TEXT[]; array_to_string flattens it so a tag is matched without unnesting.
-- It is wrapped in a function because a GIN index expression requires every
-- function in it be IMMUTABLE; the composition is deterministic for these fixed
-- text inputs, so marking the wrapper IMMUTABLE is correct (the same argument
-- resource_fts in 000091 makes). The request-path search must call this
-- function with the same argument order to hit the index.
CREATE OR REPLACE FUNCTION catalog_dataset_fts(
    name text, description text, tags text[], domain text
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(name, '')        || ' ' ||
        coalesce(description, '') || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        coalesce(domain, ''));
$$;

CREATE INDEX IF NOT EXISTS idx_catalog_datasets_search_fts
    ON catalog_datasets USING gin (catalog_dataset_fts(name, description, tags, domain));

CREATE TABLE IF NOT EXISTS catalog_dataset_sync (
    id        BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (id),
    synced_at TIMESTAMPTZ NOT NULL
);
