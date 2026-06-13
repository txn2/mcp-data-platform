-- Known-users directory (#614). NOT an authorization layer: a record of
-- people keyed by email so share pickers can resolve a name from an address.
-- Rows arrive two ways:
--   source='auth'  upserted when a person authenticates (token claims fill
--                  the name); confirmed flips TRUE on first real session.
--   source='admin' pre-added by an admin before the person has ever logged in,
--                  so they are selectable for sharing immediately.
-- Admin-entered names win: a login only fills blank name fields. See
-- pkg/user.PostgresStore.Observe.
CREATE TABLE IF NOT EXISTS users (
    email        TEXT        PRIMARY KEY,            -- normalized lowercase
    first_name   TEXT        NOT NULL DEFAULT '',
    last_name    TEXT        NOT NULL DEFAULT '',
    source       TEXT        NOT NULL DEFAULT 'auth',  -- 'auth' | 'admin'
    confirmed    BOOLEAN     NOT NULL DEFAULT FALSE,   -- TRUE once seen via a real session
    added_by     TEXT        NOT NULL DEFAULT '',      -- admin email for source='admin'
    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The share picker lists and searches by name; index both name columns.
CREATE INDEX IF NOT EXISTS idx_users_last_name ON users(last_name);
CREATE INDEX IF NOT EXISTS idx_users_first_name ON users(first_name);
