-- 000044: api_catalog_operation_embeddings
--
-- Persists the per-operation embedding vectors that semantic and
-- hybrid ranking in api_list_endpoints rank against. Before this
-- migration the toolkit held these vectors in process memory and
-- recomputed them at every spec load and every connection
-- registration (each registration triggered a fresh warm-up pass
-- through the embedding provider, repeating work whose only input
-- was spec content). Persisting collapses the warmer state machine
-- to row-existence and shares one set of vectors across every
-- connection that mounts the same catalog.
--
-- Keyed on (catalog_id, spec_name, operation_id). Connection
-- metadata is not in the key: the embedding text is a pure function
-- of the spec content. text_hash stores the SHA-256 of the source
-- text concatenated for the operation so refreshes can skip the
-- provider call for operations whose source did not change.
-- model + dim surface the provider identifier and vector
-- dimensionality at row time so a future model change can be
-- detected (today the toolkit pins 768-dim nomic-embed-text via the
-- shared embedding.Provider abstraction).
--
-- pgvector is already enabled by migration 000031_memory_records.
-- Re-enable defensively so this migration is self-contained for
-- deployments that disable the memory layer.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS api_catalog_operation_embeddings (
    catalog_id   TEXT        NOT NULL,
    spec_name    TEXT        NOT NULL,
    operation_id TEXT        NOT NULL,
    text_hash    BYTEA       NOT NULL,
    embedding    vector(768) NOT NULL,
    model        TEXT        NOT NULL DEFAULT '',
    dim          INTEGER     NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (catalog_id, spec_name, operation_id),
    FOREIGN KEY (catalog_id, spec_name)
        REFERENCES api_catalog_specs(catalog_id, spec_name)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_api_catalog_operation_embeddings_catalog_spec
    ON api_catalog_operation_embeddings (catalog_id, spec_name);
