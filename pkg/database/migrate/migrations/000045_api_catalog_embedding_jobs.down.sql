-- Reverses 000045: drop the job queue and the operation_count
-- column. The api_catalog_operation_embeddings table itself
-- (migration 000044) is left in place; downgrading the queue
-- does not invalidate persisted vectors.

DROP INDEX IF EXISTS api_catalog_embedding_jobs_history;
DROP INDEX IF EXISTS api_catalog_embedding_jobs_lease;
DROP INDEX IF EXISTS api_catalog_embedding_jobs_ready;
DROP INDEX IF EXISTS api_catalog_embedding_jobs_open;
DROP TABLE IF EXISTS api_catalog_embedding_jobs;

ALTER TABLE api_catalog_specs DROP COLUMN IF EXISTS operation_count;
