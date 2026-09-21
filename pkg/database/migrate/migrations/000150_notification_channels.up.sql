-- 000150: notification channels (#1720).
--
-- A channel is an operator-configured destination: a Slack or Mattermost
-- channel, an incoming webhook, or a named list of email addresses. It holds
-- no credential. The three HTTP kinds name an api connection, whose bot token
-- is already encrypted under that connection's credential and whose persona
-- authorization becomes the channel's, so nothing here is a secret and
-- nothing here duplicates one.
--
-- config is JSONB rather than a column per kind because each kind needs a
-- different subset -- a webhook has no target, an email list has no
-- connection -- and the shapes are refused in Go by ValidateChannel, which
-- can say which field the operator's chosen kind cannot use. A CHECK
-- constraint per kind would say only that the row is bad.
CREATE TABLE IF NOT EXISTS notification_channels (
    name       TEXT        PRIMARY KEY,
    kind       TEXT        NOT NULL
               CHECK (kind IN ('slack', 'mattermost', 'webhook', 'email')),
    config     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    enabled    BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- channel names the destination a queued row was addressed to, and is NULL
-- for every row addressed to a person alone. It is a column rather than a
-- payload field for two reasons the payload could not serve: the worker
-- claims on it, because a chat row is deliverable on a deployment with no
-- mail server, and the per-channel hourly cap counts on it.
ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS channel TEXT;

-- The cap counts a channel's recent rows and the admin history lists a
-- channel newest-first; both are this index. Partial, because the column is
-- NULL on every row the feature does not touch.
CREATE INDEX IF NOT EXISTS idx_notifications_channel_created_at
    ON notifications(channel, created_at DESC)
    WHERE channel IS NOT NULL;
