-- Platform settings: typed, admin-editable global settings sections
-- (SMTP first; designed to hold future sections). One row per section,
-- value is a JSON document; secret fields inside the value are encrypted
-- at the application layer (fieldcrypt "enc:" prefix) before insert.
CREATE TABLE IF NOT EXISTS platform_settings (
    section    TEXT        PRIMARY KEY,
    value      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    updated_by TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
