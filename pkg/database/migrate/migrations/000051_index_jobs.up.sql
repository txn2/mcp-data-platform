-- 000051: index_jobs
--
-- The source-kind-agnostic embedding-index job queue shared by the
-- platform's semantic-search consumers (pkg/indexjobs). It is the
-- generalization of api_catalog_embedding_jobs (migration 000045):
-- the queue mechanics are identical, but the unit of work is keyed
-- on an opaque (source_kind, source_id) pair instead of the
-- catalog-specific (catalog_id, spec_name) pair.
--
--   source_kind: the corpus (api_catalog, tools, prompts, ...).
--   source_id:   an opaque, consumer-defined id within the corpus.
--                api_catalog packs (catalog_id, spec_name) into it;
--                a 1:1 corpus uses the row's primary id directly.
--                The framework never parses it; only each consumer's
--                Source/Sink interpret it.
--
-- Unlike api_catalog_embedding_jobs this table carries NO foreign
-- key. Jobs are transient queue rows, not durable references: a job
-- for a source row that was deleted between enqueue and claim simply
-- finds zero items at LoadItems time and completes writing zero
-- vectors, and the reconciler will not resurrect it (the deleted
-- row contributes no expected count). A generic FK is impossible
-- anyway because the source rows live in different tables per kind.
-- The expensive, referentially-significant data (the vectors) stays
-- in each kind's own table with its own FK and ON DELETE CASCADE;
-- this queue holds only work items.
--
-- The unique partial index enforces "at most one open job per
-- (source_kind, source_id)" so producers fire-and-forget with
-- ON CONFLICT DO NOTHING. Finished rows (succeeded | failed)
-- accumulate as per-unit history the admin job views read.

CREATE TABLE IF NOT EXISTS index_jobs (
    id                BIGSERIAL   PRIMARY KEY,
    source_kind       TEXT        NOT NULL,
    source_id         TEXT        NOT NULL,
    trigger_kind      TEXT        NOT NULL,
    -- trigger_kind: write (consumer write paths)
    --             | reconciler (periodic gap detector)
    --             | manual_retry (operator force-retry; the worker
    --               skips the dedup pass so every item re-embeds).
    --             Named trigger_kind rather than "trigger" because
    --             TRIGGER is a reserved word in Postgres.
    status            TEXT        NOT NULL DEFAULT 'pending',
    -- status: pending -> running -> succeeded | failed
    attempts          INTEGER     NOT NULL DEFAULT 0,
    last_error        TEXT        NOT NULL DEFAULT '',
    next_run_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    worker_id         TEXT        NOT NULL DEFAULT '',
    lease_expires_at  TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    -- items_done: in-flight progress counter the worker bumps at
    -- chunk boundaries so a status endpoint can render "running,
    -- N/M" before the final atomic upsert commits. Reset to 0 by
    -- Claim; meaningful only while status = running.
    items_done        INTEGER     NOT NULL DEFAULT 0
);

-- At most one open job per (source_kind, source_id). Producers
-- ON CONFLICT DO NOTHING against this partial index, so a write that
-- arrives while an index pass is already pending or running
-- collapses to a no-op. Succeeded / failed rows do not block new
-- inserts, so the same unit can have many historical rows.
CREATE UNIQUE INDEX IF NOT EXISTS index_jobs_open
    ON index_jobs (source_kind, source_id)
    WHERE status IN ('pending', 'running');

-- Claim query support: workers look for the oldest pending job
-- whose backoff window has elapsed.
CREATE INDEX IF NOT EXISTS index_jobs_ready
    ON index_jobs (next_run_at)
    WHERE status = 'pending';

-- Reaper support: the lease-expiry sweep flips status=running rows
-- whose lease has elapsed back to status=pending.
CREATE INDEX IF NOT EXISTS index_jobs_lease
    ON index_jobs (lease_expires_at)
    WHERE status = 'running';

-- Admin-facing history queries (per-unit job log, "show me the last
-- N failures for this kind") order by id DESC; a covering index
-- keeps those fast as the table grows.
CREATE INDEX IF NOT EXISTS index_jobs_history
    ON index_jobs (source_kind, source_id, id DESC);
