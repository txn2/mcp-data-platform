-- OAuth Clients
CREATE TABLE IF NOT EXISTS oauth_clients (
    id              VARCHAR(32) PRIMARY KEY,
    client_id       VARCHAR(255) NOT NULL UNIQUE,
    client_secret   VARCHAR(255) NOT NULL,  -- bcrypt hashed
    name            VARCHAR(255) NOT NULL,
    redirect_uris   JSONB NOT NULL DEFAULT '[]',
    grant_types     JSONB NOT NULL DEFAULT '["authorization_code", "refresh_token"]',
    require_pkce    BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active          BOOLEAN DEFAULT true
);

CREATE INDEX IF NOT EXISTS idx_oauth_clients_client_id ON oauth_clients(client_id);
CREATE INDEX IF NOT EXISTS idx_oauth_clients_active ON oauth_clients(active);

-- Authorization Codes
CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    id              VARCHAR(32) PRIMARY KEY,
    code            VARCHAR(255) NOT NULL UNIQUE,
    client_id       VARCHAR(255) NOT NULL,
    user_id         VARCHAR(255) NOT NULL,
    user_claims     JSONB,
    code_challenge  VARCHAR(255),
    redirect_uri    TEXT NOT NULL,
    scope           TEXT,
    expires_at      TIMESTAMPTZ NOT NULL,
    used            BOOLEAN DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_auth_codes_code ON oauth_authorization_codes(code);
CREATE INDEX IF NOT EXISTS idx_oauth_auth_codes_expires ON oauth_authorization_codes(expires_at);

-- Refresh Tokens
CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    id              VARCHAR(32) PRIMARY KEY,
    token           VARCHAR(255) NOT NULL UNIQUE,
    client_id       VARCHAR(255) NOT NULL,
    user_id         VARCHAR(255) NOT NULL,
    user_claims     JSONB,
    scope           TEXT,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_token ON oauth_refresh_tokens(token);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_client ON oauth_refresh_tokens(client_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_expires ON oauth_refresh_tokens(expires_at);
