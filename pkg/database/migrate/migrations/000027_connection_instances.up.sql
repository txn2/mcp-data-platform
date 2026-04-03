-- Connection instances managed via admin API.
-- These overlay file-configured connections at startup.

CREATE TABLE connection_instances (
    kind        TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    config      JSONB       NOT NULL DEFAULT '{}',
    description TEXT        NOT NULL DEFAULT '',
    created_by  TEXT        NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (kind, name)
);
