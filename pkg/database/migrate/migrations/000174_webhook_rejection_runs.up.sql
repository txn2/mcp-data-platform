-- 000174: a webhook source's rejected requests are kept per outcome, and a run
-- of identical ones is one row (#2001).
--
-- Each source used to keep its newest 50 rejections of any outcome, so one
-- burst of rate_limited refusals from an authenticated sender deleted every
-- unauthorized row. Each (source, outcome) now keeps its own newest 50, and a
-- rejection with the same outcome and reason as that outcome's newest row,
-- within a minute of it, adds to the row: count is how many it stands for,
-- first_at the earliest of them, rejected_at the latest.
ALTER TABLE webhook_rejections
    ADD COLUMN IF NOT EXISTS count    BIGINT      NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS first_at TIMESTAMPTZ;

UPDATE webhook_rejections SET first_at = rejected_at WHERE first_at IS NULL;

ALTER TABLE webhook_rejections
    ALTER COLUMN first_at SET DEFAULT NOW(),
    ALTER COLUMN first_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_webhook_rejections_source_outcome
    ON webhook_rejections (source, outcome, id DESC);
