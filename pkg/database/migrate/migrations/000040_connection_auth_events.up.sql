-- 000040: connection_auth_events
--
-- Durable audit history for the OAuth lifecycle of every connection
-- (kind ∈ {mcp, api, future kinds}). Replaces the previous behavior
-- where revoked-refresh deletions, transient failures, and successful
-- refreshes left no trail beyond ephemeral pod logs that rotate away.
--
-- The table is append-only from the application's perspective. A
-- background prune drops rows older than the configured retention
-- (default 90 days); operators see the most recent N events per
-- connection on the admin status card. See pkg/authevents.

CREATE TABLE IF NOT EXISTS connection_auth_events (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    occurred_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    connection_kind VARCHAR(32)  NOT NULL,
    connection_name VARCHAR(255) NOT NULL,
    event_type      VARCHAR(64)  NOT NULL,
    actor           VARCHAR(255) NOT NULL DEFAULT '',
    idp_host        VARCHAR(255) NOT NULL DEFAULT '',
    detail          JSONB        NOT NULL DEFAULT '{}'::jsonb
);

-- Per-connection lookup (newest-first for the History panel) is the
-- primary access pattern. The prune job's predicate is on occurred_at
-- alone, so a second index keeps DELETE WHERE occurred_at < cutoff
-- index-only.
CREATE INDEX IF NOT EXISTS idx_connection_auth_events_conn_occurred
    ON connection_auth_events (connection_kind, connection_name, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_connection_auth_events_occurred
    ON connection_auth_events (occurred_at);
