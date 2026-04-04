-- API keys managed via admin API.
-- DB keys are loaded at startup and supplement file-configured keys.

CREATE TABLE api_keys (
    name             TEXT        PRIMARY KEY,
    key_hash         TEXT        NOT NULL,
    email            TEXT        NOT NULL DEFAULT '',
    description      TEXT        NOT NULL DEFAULT '',
    roles            JSONB       NOT NULL DEFAULT '[]',
    expires_at       TIMESTAMPTZ,
    created_by       TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
