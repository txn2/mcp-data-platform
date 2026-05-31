-- 000054: tool_embeddings
--
-- Per-tool embedding vectors backing platform_find_tools semantic
-- discovery (#440). Each row is one registered MCP tool's descriptor
-- vector (name + description + parameter-schema summary), embedded by
-- the shared indexjobs worker under source_kind = 'tools'.
--
-- Keyed on (source_id, tool_name). source_id is the platform's tool-
-- registry identifier (one logical corpus per server); tool_name is
-- the MCP tool name and the indexjobs item_id. There is NO foreign
-- key: tools are not rows in a DB table, they are registered in the
-- process from compiled-in toolkits plus admin tool-visibility config,
-- so there is nothing to reference. This mirrors index_jobs, which is
-- likewise FK-free for the same reason.
--
-- text_hash is the SHA-256 of the descriptor text, so a reindex skips
-- the embedding provider for tools whose descriptor did not change.
-- model + dim record the provider identity and dimensionality at write
-- time. The 768-dim vector matches the platform's pinned embedding
-- model (nomic-embed-text), the same dimensionality migration 000044
-- pins for api_catalog_operation_embeddings.
--
-- pgvector is enabled by migration 000031; re-enable defensively so
-- this migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS tool_embeddings (
    source_id   TEXT        NOT NULL,
    tool_name   TEXT        NOT NULL,
    text_hash   BYTEA       NOT NULL,
    embedding   vector(768) NOT NULL,
    model       TEXT        NOT NULL DEFAULT '',
    dim         INTEGER     NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_id, tool_name)
);

CREATE INDEX IF NOT EXISTS idx_tool_embeddings_source
    ON tool_embeddings (source_id);
