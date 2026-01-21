-- E2E Test Database Initialization
-- Creates schema for OAuth and Audit tables

-- OAuth tables (simplified for testing)
CREATE TABLE IF NOT EXISTS oauth_clients (
    id VARCHAR(255) PRIMARY KEY,
    secret_hash VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    redirect_uris TEXT[] NOT NULL DEFAULT '{}',
    grant_types TEXT[] NOT NULL DEFAULT '{}',
    response_types TEXT[] NOT NULL DEFAULT '{}',
    scopes TEXT[] NOT NULL DEFAULT '{}',
    token_endpoint_auth_method VARCHAR(50) DEFAULT 'client_secret_basic',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    code VARCHAR(255) PRIMARY KEY,
    client_id VARCHAR(255) NOT NULL REFERENCES oauth_clients(id),
    user_id VARCHAR(255) NOT NULL,
    redirect_uri TEXT NOT NULL,
    scope TEXT,
    code_challenge VARCHAR(255),
    code_challenge_method VARCHAR(10),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS oauth_access_tokens (
    token VARCHAR(255) PRIMARY KEY,
    client_id VARCHAR(255) NOT NULL REFERENCES oauth_clients(id),
    user_id VARCHAR(255) NOT NULL,
    scope TEXT,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    token VARCHAR(255) PRIMARY KEY,
    access_token VARCHAR(255) NOT NULL REFERENCES oauth_access_tokens(token),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Audit tables
CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGSERIAL PRIMARY KEY,
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    request_id VARCHAR(255) NOT NULL,
    user_id VARCHAR(255),
    user_email VARCHAR(255),
    persona_name VARCHAR(100),
    tool_name VARCHAR(255) NOT NULL,
    toolkit_kind VARCHAR(100),
    toolkit_name VARCHAR(255),
    connection_name VARCHAR(255),
    success BOOLEAN NOT NULL,
    error_message TEXT,
    duration_ms INTEGER,
    metadata JSONB
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp ON audit_logs(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tool_name ON audit_logs(tool_name);

-- Insert test OAuth client
INSERT INTO oauth_clients (id, secret_hash, name, redirect_uris, grant_types, scopes)
VALUES (
    'e2e-test-client',
    '$2a$10$abcdefghijklmnopqrstuvwxyz123456789', -- bcrypt hash placeholder
    'E2E Test Client',
    ARRAY['http://localhost:3000/callback'],
    ARRAY['authorization_code', 'refresh_token'],
    ARRAY['read', 'write']
) ON CONFLICT (id) DO NOTHING;

-- Grant statement for test purposes
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO platform;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO platform;
