-- Reverse 000057: drop the retention support index. The retainer's
-- DELETE still works without it (it falls back to a sequential scan),
-- so dropping the index only affects purge performance, not correctness.
DROP INDEX IF EXISTS index_jobs_retention;
