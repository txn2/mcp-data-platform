-- Persistent OAuth token storage for the HTTP API gateway's
-- authorization_code grant. Parallel to gateway_oauth_tokens
-- (the MCP gateway's table) — kept separate so the two toolkit
-- families' connection-name spaces cannot collide. Schema is
-- intentionally identical so future shared infrastructure can
-- consume both with the same row shape.
CREATE TABLE IF NOT EXISTS apigateway_oauth_tokens (
    connection_name    VARCHAR(255) PRIMARY KEY,
    access_token       TEXT,
    refresh_token      TEXT,
    expires_at         TIMESTAMPTZ,
    refresh_expires_at TIMESTAMPTZ,
    scope              TEXT NOT NULL DEFAULT '',
    authenticated_by   VARCHAR(255) NOT NULL DEFAULT '',
    authenticated_at   TIMESTAMPTZ,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
