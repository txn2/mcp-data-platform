-- Reverses 000140: drop the graphql schema and operation-embedding
-- tables. The pgvector extension is left enabled because the memory
-- layer (migration 000031) still depends on it.

DROP TABLE IF EXISTS graphql_operation_embeddings;
DROP TABLE IF EXISTS graphql_connection_schemas;
