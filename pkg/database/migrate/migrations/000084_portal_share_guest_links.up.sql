-- One-time view links for email-share recipients without an account (#1001).
-- Each row is a single-use, short-lived token emailed to the share's stored
-- recipient address. Only the token's SHA-256 is stored; the plaintext exists
-- solely in the emailed URL. Single use is enforced by the atomic claim
-- (UPDATE ... SET used_at WHERE used_at IS NULL ... RETURNING), so a
-- forwarded or replayed link is dead after its first use.
CREATE TABLE IF NOT EXISTS portal_share_guest_links (
    id         TEXT        PRIMARY KEY,
    share_id   TEXT        NOT NULL,
    token_hash TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ
);

-- Backs the per-share issue cap (count of links created inside a window).
CREATE INDEX IF NOT EXISTS idx_portal_share_guest_links_share_created
    ON portal_share_guest_links (share_id, created_at);
