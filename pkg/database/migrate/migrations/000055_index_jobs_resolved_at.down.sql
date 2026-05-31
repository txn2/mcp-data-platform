-- 000055 down: drop the unresolved-failure triage index, then the
-- resolved_at column.

DROP INDEX IF EXISTS index_jobs_unresolved_failed;

ALTER TABLE index_jobs
    DROP COLUMN IF EXISTS resolved_at;
