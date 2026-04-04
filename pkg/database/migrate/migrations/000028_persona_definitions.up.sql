-- Persona definitions managed via admin API.
-- DB personas override file-configured personas at startup.

CREATE TABLE persona_definitions (
    name             TEXT        PRIMARY KEY,
    display_name     TEXT        NOT NULL DEFAULT '',
    description      TEXT        NOT NULL DEFAULT '',
    roles            JSONB       NOT NULL DEFAULT '[]',
    tools_allow      JSONB       NOT NULL DEFAULT '[]',
    tools_deny       JSONB       NOT NULL DEFAULT '[]',
    connections_allow JSONB      NOT NULL DEFAULT '[]',
    connections_deny  JSONB      NOT NULL DEFAULT '[]',
    context          JSONB       NOT NULL DEFAULT '{}',
    priority         INTEGER     NOT NULL DEFAULT 0,
    created_by       TEXT        NOT NULL DEFAULT '',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
