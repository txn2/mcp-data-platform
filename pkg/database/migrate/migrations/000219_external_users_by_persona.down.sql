UPDATE persona_definitions SET portal = portal - 'uploads'
WHERE jsonb_typeof(portal->'uploads') = 'boolean';

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
    converted_at   TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS external_accounts_email_idx ON external_accounts (lower(email));

CREATE TABLE IF NOT EXISTS external_signin_links (
    token_hash  TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES external_accounts (id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    claimed_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS external_signin_links_account_idx ON external_signin_links (account_id, created_at);
