ALTER TABLE gateway_oauth_tokens
    DROP COLUMN IF EXISTS refresh_expires_at;
