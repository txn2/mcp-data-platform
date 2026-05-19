-- 000046: drop the embedded_so_far counter.
ALTER TABLE api_catalog_embedding_jobs DROP COLUMN IF EXISTS embedded_so_far;
