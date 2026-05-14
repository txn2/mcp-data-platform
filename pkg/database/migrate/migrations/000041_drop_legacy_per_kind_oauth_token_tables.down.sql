-- Recreate the per-kind OAuth token tables. The schema mirrors the
-- post-000037 (gateway) and post-000038 (apigateway) state, so a
-- rollback that re-runs 000037 / 000038 finds the tables already at
-- the expected shape. The rollback DOES NOT restore previously
-- written rows — those live in connection_oauth_tokens, which the
-- forward migration leaves untouched.

CREATE TABLE IF NOT EXISTS gateway_oauth_tokens (
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
