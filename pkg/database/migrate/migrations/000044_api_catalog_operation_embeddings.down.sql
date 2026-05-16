-- Reverses 000044: drop the operation embeddings table. The
-- pgvector extension is left enabled because the memory layer
-- (migration 000031) still depends on it.

DROP TABLE IF EXISTS api_catalog_operation_embeddings;
