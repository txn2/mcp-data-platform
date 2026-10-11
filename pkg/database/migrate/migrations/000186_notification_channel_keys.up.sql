-- 000186: suppression state for repeating channel alerts (#1722).
--
-- One row per (channel, key): when a send with that key last went through,
-- the window it was measured against, and how many sends inside the window
-- were absorbed since. A keyed send reads and writes its row in the same
-- transaction that writes (or does not write) its queue rows, under the
-- channel's advisory lock, so two replicas sending the same alert at once
-- deliver it once.
--
-- Nothing here is content: a key is the sender's label for an alert.
CREATE TABLE IF NOT EXISTS notification_channel_keys (
    channel             TEXT        NOT NULL,
    key                 TEXT        NOT NULL,
    last_sent_at        TIMESTAMPTZ NOT NULL,
    window_seconds      INTEGER     NOT NULL,
    suppressed          INTEGER     NOT NULL DEFAULT 0,
    first_suppressed_at TIMESTAMPTZ,
    PRIMARY KEY (channel, key)
);

-- The retention pass deletes keys whose window ended long ago.
CREATE INDEX IF NOT EXISTS idx_notification_channel_keys_last_sent
    ON notification_channel_keys(last_sent_at);

-- A run's page lists the posts that run made (#1723), read from the queue by
-- the run recorded on each post's document. Partial, as the channel index is:
-- only rows sent to a channel carry a document.
CREATE INDEX IF NOT EXISTS idx_notifications_document_run
    ON notifications ((payload->'document'->'source'->>'run_id'))
    WHERE channel IS NOT NULL;
