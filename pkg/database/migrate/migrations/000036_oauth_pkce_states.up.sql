-- 000036: oauth_pkce_states
--
-- Server-side hold for in-flight authorization_code + PKCE flows.
-- One row per oauth-start; deleted by /oauth/callback (or by GC).
--
-- Multi-replica deployments need this to be DB-backed: oauth-start may
-- land on replica A while the upstream's redirect lands the callback
-- on replica B. An in-memory store would 400 every cross-replica flow.
--
-- Rows live for at most pkceTTL (10 minutes by default in the handler);
-- a periodic delete sweeps expired rows, but the partial index gives
-- the take() lookup constant-time access regardless.

CREATE TABLE IF NOT EXISTS oauth_pkce_states (
    state         VARCHAR(255) PRIMARY KEY,
    connection    VARCHAR(255) NOT NULL,
    code_verifier TEXT         NOT NULL,
    started_by    VARCHAR(255) NOT NULL DEFAULT '',
    return_url    TEXT         NOT NULL DEFAULT '',
    redirect_uri  TEXT         NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at    TIMESTAMPTZ  NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_oauth_pkce_states_expires_at
    ON oauth_pkce_states (expires_at);
