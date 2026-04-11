-- resources stores metadata for human-uploaded reference material (samples,
-- playbooks, templates, references). Blobs live in S3; this table is the
-- source of truth for metadata, permissions, and URI resolution.
CREATE TABLE IF NOT EXISTS resources (
    id              TEXT PRIMARY KEY,
    scope           TEXT NOT NULL CHECK (scope IN ('global', 'persona', 'user')),
    scope_id        TEXT,               -- persona name or user sub; NULL for global
    category        TEXT NOT NULL,       -- 'samples', 'playbooks', 'templates', 'references', etc.
    filename        TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    description     TEXT NOT NULL,       -- what is this and what should the agent do with it
    mime_type       TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL,
    s3_key          TEXT NOT NULL,
    uri             TEXT NOT NULL UNIQUE, -- canonical resource URI, generated server-side
    tags            TEXT[] NOT NULL DEFAULT '{}',
    uploader_sub    TEXT NOT NULL,       -- Keycloak sub of who uploaded
    uploader_email  TEXT NOT NULL DEFAULT '', -- email for display purposes
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT resources_scope_id_required CHECK (
        (scope = 'global' AND scope_id IS NULL) OR
        (scope IN ('persona', 'user') AND scope_id IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_resources_scope ON resources (scope, scope_id);
CREATE INDEX IF NOT EXISTS idx_resources_uploader ON resources (uploader_sub);
CREATE INDEX IF NOT EXISTS idx_resources_category ON resources (category);
CREATE INDEX IF NOT EXISTS idx_resources_uri ON resources (uri);
