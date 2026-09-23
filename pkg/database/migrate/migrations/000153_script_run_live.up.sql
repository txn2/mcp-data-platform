-- What a script run hands back and reports while it executes (#1845, #1847).
--
-- result is the JSON value a run returns with platform.result: a small answer
-- for its caller, kept on the run row and nowhere else, so it follows the run's
-- retention and is never an asset.
--
-- The progress columns hold the latest platform.progress report, written by the
-- worker under the run's lease every few seconds while the run executes, along
-- with the log printed so far in the existing log_text. They stay on the row
-- after the run ends, as the last thing it reported.
--
-- cancel_requested_at/_by record a request to stop a running run. The worker
-- holding it reads the flag in the same statement that writes its progress, so
-- a cancel reaches whichever replica is executing the run without a second
-- channel. A pending run is canceled outright and never claimed.
ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS result              JSONB,
    ADD COLUMN IF NOT EXISTS progress_message    TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS progress_done       BIGINT,
    ADD COLUMN IF NOT EXISTS progress_total      BIGINT,
    ADD COLUMN IF NOT EXISTS progress_at         TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS cancel_requested_by TEXT        NOT NULL DEFAULT '';

-- status gains 'canceled', terminal like succeeded and failed.
ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_status_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_status_check
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped_overlap', 'canceled'));
