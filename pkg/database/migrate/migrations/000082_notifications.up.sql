-- Notification delivery queue. Rows are enqueued by share/comment triggers
-- and claimed by the send worker with lease-based locking (status +
-- locked_until), following the indexjobs queue pattern. scheduled_for
-- carries the digest window for mode 'daily'; immediate rows are scheduled
-- at enqueue time.
CREATE TABLE IF NOT EXISTS notifications (
    id            BIGSERIAL   PRIMARY KEY,
    recipient     TEXT        NOT NULL,
    category      TEXT        NOT NULL,
    payload       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    digest        BOOLEAN     NOT NULL DEFAULT FALSE,
    status        TEXT        NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    attempts      INTEGER     NOT NULL DEFAULT 0,
    last_error    TEXT        NOT NULL DEFAULT '',
    scheduled_for TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_until  TIMESTAMPTZ,
    sent_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The worker's claim query filters on status + scheduled_for.
CREATE INDEX IF NOT EXISTS idx_notifications_status_scheduled
    ON notifications(status, scheduled_for);
-- Digest grouping selects a recipient's pending digest rows.
CREATE INDEX IF NOT EXISTS idx_notifications_recipient ON notifications(recipient);
