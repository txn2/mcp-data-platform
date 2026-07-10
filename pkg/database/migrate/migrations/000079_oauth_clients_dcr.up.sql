-- Mark dynamically-registered (DCR) clients so unused registrations from the
-- unauthenticated /register endpoint can be reaped on a TTL without ever
-- touching pre-registered (config-file) clients. Pre-existing rows default to
-- false (pre-registered / unknown), so only clients created through DCR after
-- this migration are eligible for cleanup.
ALTER TABLE oauth_clients ADD COLUMN IF NOT EXISTS dcr BOOLEAN NOT NULL DEFAULT false;

-- last_used_at records the first (and subsequent) token issuance for a client.
-- It is the race-free "never completed a token exchange" signal used by the
-- DCR cleanup: a NULL value means no token was ever issued (an abandoned
-- registration), whereas a client that completed an authorization-code flow
-- has a non-NULL value and is never reaped — even during the brief tokenless
-- window of refresh-token rotation or after its refresh token later expires.
ALTER TABLE oauth_clients ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;

-- Partial index supporting the cleanup predicate
-- (dcr = true AND last_used_at IS NULL AND created_at < cutoff).
CREATE INDEX IF NOT EXISTS idx_oauth_clients_dcr_unused
    ON oauth_clients (created_at)
    WHERE dcr = true AND last_used_at IS NULL;
