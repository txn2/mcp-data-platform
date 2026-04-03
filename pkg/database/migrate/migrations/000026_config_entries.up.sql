-- Replace the full-YAML-blob config_versions table with granular key/value config_entries.
-- DB entries override file config for whitelisted keys, with per-key hot-reload.

DROP INDEX IF EXISTS idx_config_versions_created;
DROP INDEX IF EXISTS idx_config_versions_active;
DROP TABLE IF EXISTS config_versions;

CREATE TABLE config_entries (
    key         TEXT        PRIMARY KEY,
    value_text  TEXT        NOT NULL,
    updated_by  TEXT        NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE config_changelog (
    id          SERIAL      PRIMARY KEY,
    key         TEXT        NOT NULL,
    action      TEXT        NOT NULL,
    value_text  TEXT,
    changed_by  TEXT        NOT NULL DEFAULT '',
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_config_changelog_key ON config_changelog(key);
CREATE INDEX idx_config_changelog_changed_at ON config_changelog(changed_at DESC);
