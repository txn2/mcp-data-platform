-- #2639: a short-lived, single-use link an agent PUTs a file to from its
-- shell, filed as a managed resource at the address it was issued for. The
-- link's token is a bearer credential for one write and is never stored: the
-- row is found by its SHA-256. claims are the issuing person's resource claims,
-- which the write is made with.
CREATE TABLE IF NOT EXISTS resource_upload_links (
    id            TEXT PRIMARY KEY,
    token_sha256  TEXT NOT NULL UNIQUE,
    user_id       TEXT NOT NULL,
    user_email    TEXT NOT NULL DEFAULT '',
    claims        JSONB NOT NULL,
    scope         TEXT NOT NULL,
    scope_id      TEXT NOT NULL DEFAULT '',
    path          TEXT NOT NULL DEFAULT '',
    filename      TEXT NOT NULL,
    display_name  TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    tags          TEXT[] NOT NULL DEFAULT '{}',
    content_type  TEXT NOT NULL,
    if_exists     TEXT NOT NULL DEFAULT '',
    max_bytes     BIGINT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    resource_id   TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_resource_upload_links_expires ON resource_upload_links (expires_at);
