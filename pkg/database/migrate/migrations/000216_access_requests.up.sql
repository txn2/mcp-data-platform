-- A request for access to an asset, collection or prompt from someone who
-- opened a link to it they cannot open (#2090). The owner approves it, which
-- creates an ordinary person share, or declines it. A request expires after
-- the access_requests setting's period.
CREATE TABLE IF NOT EXISTS access_requests (
    id               TEXT PRIMARY KEY,
    target_type      TEXT NOT NULL CHECK (target_type IN ('asset', 'collection', 'prompt')),
    target_id        TEXT NOT NULL,
    requester_id     TEXT NOT NULL,
    requester_email  TEXT NOT NULL,
    owner_email      TEXT NOT NULL DEFAULT '',
    message          TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'declined')),
    permission       TEXT NOT NULL DEFAULT '',
    decided_by       TEXT NOT NULL DEFAULT '',
    decided_at       TIMESTAMPTZ,
    share_id         TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at       TIMESTAMPTZ NOT NULL
);

-- One pending request per requester and item.
CREATE UNIQUE INDEX IF NOT EXISTS access_requests_pending_idx
    ON access_requests (target_type, target_id, requester_id) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS access_requests_owner_idx ON access_requests (lower(owner_email), status, created_at DESC);
