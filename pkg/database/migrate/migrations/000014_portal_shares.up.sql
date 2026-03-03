CREATE TABLE IF NOT EXISTS portal_shares (
    id              TEXT PRIMARY KEY,
    asset_id        TEXT NOT NULL REFERENCES portal_assets(id),
    token           TEXT NOT NULL UNIQUE,
    created_by      TEXT NOT NULL,
    expires_at      TIMESTAMPTZ,
    revoked         BOOLEAN NOT NULL DEFAULT FALSE,
    access_count    INTEGER NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_portal_shares_asset_id ON portal_shares(asset_id);
CREATE INDEX IF NOT EXISTS idx_portal_shares_token ON portal_shares(token);
CREATE INDEX IF NOT EXISTS idx_portal_shares_created_by ON portal_shares(created_by);
