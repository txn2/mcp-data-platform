-- Persistent storage for gateway OAuth tokens. Used by the
-- authorization_code + PKCE flow so a one-time browser-based
-- authentication grants long-running access (refresh tokens
-- silently mint new access tokens for cron jobs and background
-- workloads). One row per gateway connection — v1 stores a single
-- shared identity per connection rather than per-user tokens.
CREATE TABLE IF NOT EXISTS gateway_oauth_tokens (
    connection_name   VARCHAR(255) PRIMARY KEY,
    access_token      TEXT,
    refresh_token     TEXT,
    expires_at        TIMESTAMPTZ,
    scope             TEXT NOT NULL DEFAULT '',
    authenticated_by  VARCHAR(255) NOT NULL DEFAULT '',
    authenticated_at  TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
