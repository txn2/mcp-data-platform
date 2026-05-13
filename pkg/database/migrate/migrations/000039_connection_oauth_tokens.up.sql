-- 000039: connection_oauth_tokens
--
-- Single shared table for OAuth 2.1 authorization_code tokens across
-- ALL connection kinds (MCP gateway, HTTP API gateway, future kinds).
-- Replaces the two per-kind tables (gateway_oauth_tokens and
-- apigateway_oauth_tokens) that previously held the same row shape
-- in parallel — the duplication produced two divergent codepaths
-- (handlers, stores, refresh logic, frontend hooks) and made subtle
-- bugs hard to find.
--
-- The old tables are NOT dropped here. A follow-up migration drops
-- them after one stable release. This gives an emergency rollback
-- path that doesn't require restoring tokens from a backup.

CREATE TABLE IF NOT EXISTS connection_oauth_tokens (
    connection_kind     VARCHAR(32)  NOT NULL,
    connection_name     VARCHAR(255) NOT NULL,
    access_token        TEXT,
    refresh_token       TEXT,
    expires_at          TIMESTAMPTZ,
    refresh_expires_at  TIMESTAMPTZ,
    scope               TEXT         NOT NULL DEFAULT '',
    authenticated_by    VARCHAR(255) NOT NULL DEFAULT '',
    authenticated_at    TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (connection_kind, connection_name)
);

-- Backfill from gateway_oauth_tokens (MCP gateway kind). The source
-- table predates the refresh_expires_at column (added in 000037), so
-- we map directly — rows that pre-date 000037 have NULL refresh_
-- expires_at and stay NULL after backfill, which is correct.
INSERT INTO connection_oauth_tokens
    (connection_kind, connection_name, access_token, refresh_token,
     expires_at, refresh_expires_at, scope, authenticated_by,
     authenticated_at, updated_at)
SELECT 'mcp', connection_name, access_token, refresh_token,
       expires_at, refresh_expires_at, scope, authenticated_by,
       authenticated_at, updated_at
  FROM gateway_oauth_tokens
ON CONFLICT (connection_kind, connection_name) DO NOTHING;

-- Backfill from apigateway_oauth_tokens (HTTP API gateway kind).
-- Schema is already aligned (refresh_expires_at present from
-- migration 000038), so columns map 1:1.
INSERT INTO connection_oauth_tokens
    (connection_kind, connection_name, access_token, refresh_token,
     expires_at, refresh_expires_at, scope, authenticated_by,
     authenticated_at, updated_at)
SELECT 'api', connection_name, access_token, refresh_token,
       expires_at, refresh_expires_at, scope, authenticated_by,
       authenticated_at, updated_at
  FROM apigateway_oauth_tokens
ON CONFLICT (connection_kind, connection_name) DO NOTHING;

-- Add connection_kind to oauth_pkce_states so the unified callback
-- can dispatch by kind. Existing rows pre-date this migration and
-- belong to MCP-only flows (the only kind that used this table at
-- the time those rows were written); default 'mcp' is the right
-- starting state for in-flight rows the migration sees.
ALTER TABLE oauth_pkce_states
    ADD COLUMN IF NOT EXISTS connection_kind VARCHAR(32) NOT NULL DEFAULT 'mcp';
