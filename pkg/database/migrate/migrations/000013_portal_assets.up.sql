CREATE TABLE IF NOT EXISTS portal_assets (
    id              TEXT PRIMARY KEY,
    owner_id        TEXT NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    content_type    TEXT NOT NULL,
    s3_bucket       TEXT NOT NULL,
    s3_key          TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL DEFAULT 0,
    tags            JSONB NOT NULL DEFAULT '[]',
    provenance      JSONB NOT NULL DEFAULT '{}',
    session_id      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_portal_assets_owner_id ON portal_assets(owner_id);
CREATE INDEX IF NOT EXISTS idx_portal_assets_created_at ON portal_assets(created_at);
CREATE INDEX IF NOT EXISTS idx_portal_assets_content_type ON portal_assets(content_type);
CREATE INDEX IF NOT EXISTS idx_portal_assets_deleted_at ON portal_assets(deleted_at);
