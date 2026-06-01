-- 000057: index_jobs retention support
--
-- index_jobs accumulates one finished row per reconciler sweep per unit
-- (every ReconcilerInterval, on every replica), so succeeded history
-- grows without bound; the reaper only releases leases and never deletes.
-- pkg/indexjobs.Retainer adds the missing sweep: it periodically deletes
-- finished history older than a retention window via Store.PurgeTerminal,
-- whose predicate is
--
--   completed_at < cutoff
--   AND (status = 'succeeded'
--        OR (status = 'failed' AND resolved_at IS NOT NULL))
--
-- so it forgets only safely-finished rows and never an open failure or an
-- in-flight job.
--
-- This partial index keeps that DELETE cheap as the succeeded bucket
-- grows: it covers exactly the purgeable set, ordered by completed_at so
-- the cutoff comparison is a range scan rather than a sequential scan of
-- the whole table. It mirrors the existing partial indexes for the open,
-- ready, and unresolved-failed sets (migrations 000051, 000055).
CREATE INDEX IF NOT EXISTS index_jobs_retention
    ON index_jobs (completed_at)
    WHERE status = 'succeeded'
       OR (status = 'failed' AND resolved_at IS NOT NULL);
