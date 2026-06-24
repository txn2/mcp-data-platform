-- platform_backfills records one-time Go-level data backfills that have run, so a
-- guarded startup task (for example the #664 Phase 5 knowledge-page reference
-- backfill) executes once rather than on every boot. A SQL migration cannot run
-- the Go reconcile logic itself, so the sentinel lives here and the application
-- checks/sets it.
CREATE TABLE IF NOT EXISTS platform_backfills (
    name         TEXT PRIMARY KEY,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
