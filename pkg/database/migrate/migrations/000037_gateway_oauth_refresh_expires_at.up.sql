-- Add refresh_expires_at to gateway_oauth_tokens. Captures the IdP's
-- self-reported refresh-token lifetime (Keycloak's refresh_expires_in
-- and similar) so the admin UI can surface a real deadline instead of
-- "—". Optional in the OAuth 2.1 spec, so NULL is the natural value
-- for IdPs that don't disclose it.
ALTER TABLE gateway_oauth_tokens
    ADD COLUMN IF NOT EXISTS refresh_expires_at TIMESTAMPTZ;
