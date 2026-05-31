-- 000055: index_jobs.resolved_at
--
-- A terminal `failed` row in index_jobs is permanent: nothing in the
-- queue ever clears it. The admin Indexing dashboard groups failed
-- rows into a triage panel, so a failure from an hour ago (an unknown
-- kind during a rolling deploy, an embedding-provider timeout that
-- exhausted MaxAttempts) sits there forever even after the unit was
-- re-indexed successfully, and clicking Retry does nothing visible
-- because Reindex enqueues a fresh job without touching the old row.
--
-- resolved_at is the "this failure no longer reflects the unit's
-- current state" marker. It is stamped two ways:
--
--   1. Automatically, when a later job for the same
--      (source_kind, source_id) succeeds: Store.Complete resolves
--      every still-open failed row for the unit, so a superseded
--      failure self-clears the moment the unit is healthy again.
--   2. Explicitly, by the dashboard's "dismiss" action
--      (Store.ResolveFailures), the operator escape hatch for a
--      failure that will never be superseded (e.g. a removed
--      consumer's leftover rows).
--
-- The triage surface reads only unresolved failures
-- (status='failed' AND resolved_at IS NULL); resolved rows stay in
-- the table as history. The column is nullable with no default so
-- every existing failed row is "unresolved" until something resolves
-- it, which is the correct interpretation for rows that predate this
-- migration.

ALTER TABLE index_jobs
    ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ;

-- Triage lookup support: the dashboard lists unresolved failures per
-- kind (and the per-unit auto-resolve sweep targets the same set). A
-- partial index keeps that query cheap as succeeded/resolved history
-- accumulates, mirroring the existing partial indexes for the open
-- and ready job sets.
CREATE INDEX IF NOT EXISTS index_jobs_unresolved_failed
    ON index_jobs (source_kind, source_id)
    WHERE status = 'failed' AND resolved_at IS NULL;
