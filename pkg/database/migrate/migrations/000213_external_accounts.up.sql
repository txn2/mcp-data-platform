-- External personas and the accounts the platform creates for them (#2092).
--
-- A persona marked external is held to limits its portal settings cannot
-- raise: no MCP tools, keys or directory, and only what is shared with its
-- people. Its people do not come from the identity provider: an administrator
-- creates each account here, and the person signs in with a one-time link the
-- platform emails, which opens a portal session for the account.
ALTER TABLE persona_definitions ADD COLUMN IF NOT EXISTS external BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS external_accounts (
    id             TEXT PRIMARY KEY,
    email          TEXT NOT NULL,
    name           TEXT NOT NULL DEFAULT '',
    persona        TEXT NOT NULL,
    expires_at     TIMESTAMPTZ,
    suspended_at   TIMESTAMPTZ,
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_sign_in_at TIMESTAMPTZ,
    -- Set when the person first signs in through the identity provider: the
    -- account is closed and the person is internal from then on.
    converted_at   TIMESTAMPTZ
);

-- One account per address, compared as the share recipient match compares it.
CREATE UNIQUE INDEX IF NOT EXISTS external_accounts_email_idx ON external_accounts (lower(email));

-- The one-time sign-in links: the invitation and every link requested from
-- the sign-in screen. Only the token's hash is kept; a claimed link is dead.
CREATE TABLE IF NOT EXISTS external_signin_links (
    token_hash  TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES external_accounts (id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    claimed_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS external_signin_links_account_idx ON external_signin_links (account_id, created_at);
