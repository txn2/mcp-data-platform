-- 000045: api_catalog_embedding_jobs + api_catalog_specs.operation_count
--
-- Replaces the synchronous, click-driven embedding pass that
-- migration 000044 left in place with a Postgres-backed job queue.
-- The previous design ran ComputeOperationEmbeddings inline inside
-- the admin HTTP handler at spec-upsert and re-embed time. For any
-- non-trivial spec this risked ingress / gateway timeouts, gave
-- the operator no visibility while the request was in flight, lost
-- error context to silent 502s, and required a manual "Re-embed"
-- click whenever something went wrong.
--
-- The new model:
--
--   1. Every mutation of a spec (write, upload, refresh, clone)
--      atomically inserts a job row alongside the spec content.
--   2. Worker goroutines across every running pod race for jobs
--      via SELECT ... FOR UPDATE SKIP LOCKED, take a time-bounded
--      lease, embed the spec's operations, write vectors, mark
--      the job succeeded. Pod crashes mid-embed expire the lease;
--      another worker picks the job up.
--   3. A reconciler runs on every pod boot AND on a periodic
--      tick. It looks at every spec row, compares the spec's
--      operation_count against the count of rows in
--      api_catalog_operation_embeddings, and enqueues a job for
--      any spec where they disagree. This is the "embedding
--      survives anything" guarantee: a spec written before the
--      embedding provider was configured, vectors lost to a
--      provider outage, or any other gap converges to a fully
--      indexed catalog without operator action.
--
-- The unique partial index enforces "at most one open job per
-- (catalog, spec)" so producers can fire-and-forget with
-- ON CONFLICT DO NOTHING. Finished rows (status in succeeded |
-- failed) accumulate as a per-spec history; an admin job-history
-- view reads them to surface "this spec was re-indexed 3 times
-- yesterday, here is the last error."
--
-- operation_count on api_catalog_specs is the reconciler's
-- expected-work column. Admin handlers set it on every spec write
-- to the number of operations buildOperationIndex parses out of
-- the spec content. The reconciler joins this against
-- api_catalog_operation_embeddings to find gaps. Without it the
-- reconciler would have to parse every spec on every tick (slow,
-- and circular: parsing is what produces the work the queue
-- distributes).

CREATE TABLE IF NOT EXISTS api_catalog_embedding_jobs (
    id                BIGSERIAL   PRIMARY KEY,
    catalog_id        TEXT        NOT NULL,
    spec_name         TEXT        NOT NULL,
    kind              TEXT        NOT NULL,
    -- kind: spec_write (set by admin write paths)
    --     | reconciler (set by the periodic gap detector)
    --     | manual_retry (set by the force-retry admin endpoint
    --       for the operator-driven "model swapped externally"
    --       escape hatch).
    status            TEXT        NOT NULL DEFAULT 'pending',
    -- status: pending -> running -> succeeded | failed
    --   pending: visible to claim queries
    --   running: a worker holds the lease (lease_expires_at set)
    --   succeeded: terminal, vectors are current
    --   failed: terminal, attempts exhausted; last_error explains
    attempts          INTEGER     NOT NULL DEFAULT 0,
    last_error        TEXT        NOT NULL DEFAULT '',
    next_run_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    worker_id         TEXT        NOT NULL DEFAULT '',
    lease_expires_at  TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    FOREIGN KEY (catalog_id, spec_name)
        REFERENCES api_catalog_specs(catalog_id, spec_name)
        ON DELETE CASCADE
);

-- At most one open job per (catalog, spec). Producers
-- ON CONFLICT DO NOTHING against this partial index, so a spec
-- edit that arrives while an embed is already pending or running
-- collapses to a no-op instead of stacking duplicate jobs.
-- Succeeded / failed rows do not block new inserts, so the same
-- spec can have many historical rows.
CREATE UNIQUE INDEX IF NOT EXISTS api_catalog_embedding_jobs_open
    ON api_catalog_embedding_jobs (catalog_id, spec_name)
    WHERE status IN ('pending', 'running');

-- Claim query support: workers look for the oldest pending job
-- whose backoff window has elapsed.
CREATE INDEX IF NOT EXISTS api_catalog_embedding_jobs_ready
    ON api_catalog_embedding_jobs (next_run_at)
    WHERE status = 'pending';

-- Reaper support: the lease-expiry sweep flips status=running
-- rows whose lease has elapsed back to status=pending.
CREATE INDEX IF NOT EXISTS api_catalog_embedding_jobs_lease
    ON api_catalog_embedding_jobs (lease_expires_at)
    WHERE status = 'running';

-- Admin-facing history queries (per-spec job log, "show me the
-- last 50 failures") order by id DESC; a covering index keeps
-- those fast even as the table grows.
CREATE INDEX IF NOT EXISTS api_catalog_embedding_jobs_history
    ON api_catalog_embedding_jobs (catalog_id, spec_name, id DESC);

ALTER TABLE api_catalog_specs
    ADD COLUMN IF NOT EXISTS operation_count INTEGER NOT NULL DEFAULT 0;
