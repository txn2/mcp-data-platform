DROP INDEX IF EXISTS idx_config_changelog_changed_at;
DROP INDEX IF EXISTS idx_config_changelog_key;
DROP TABLE IF EXISTS config_changelog;

DROP TABLE IF EXISTS config_entries;

-- Recreate the original config_versions table for rollback.
CREATE TABLE IF NOT EXISTS config_versions (
    id          SERIAL PRIMARY KEY,
    version     INTEGER NOT NULL,
    config_yaml TEXT NOT NULL,
    author      TEXT NOT NULL DEFAULT '',
    comment     TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    is_active   BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_config_versions_active
    ON config_versions (is_active) WHERE is_active = TRUE;

CREATE INDEX IF NOT EXISTS idx_config_versions_created
    ON config_versions (created_at DESC);
