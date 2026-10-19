-- #2175: the single-use tokens a managed script has verified. A token marked
-- single use is recorded here the first time it verifies, so its second use
-- is refused on every replica; a row is swept once its token has expired.
CREATE TABLE IF NOT EXISTS script_token_uses (
    token_id   TEXT        PRIMARY KEY,
    used_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_script_token_uses_expires
    ON script_token_uses(expires_at);
