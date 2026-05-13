-- Reverse migration 000039. The old per-kind tables are still
-- present (this migration intentionally did not drop them), so on
-- down we simply remove the unified table and the added
-- pkce-state column. No token data is lost: pre-migration rows
-- still live in gateway_oauth_tokens and apigateway_oauth_tokens.
DROP TABLE IF EXISTS connection_oauth_tokens;

ALTER TABLE oauth_pkce_states
    DROP COLUMN IF EXISTS connection_kind;
