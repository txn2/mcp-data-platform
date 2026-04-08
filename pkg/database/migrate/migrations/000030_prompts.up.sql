CREATE TABLE IF NOT EXISTS prompts (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT        NOT NULL UNIQUE,
    display_name TEXT        NOT NULL DEFAULT '',
    description  TEXT        NOT NULL DEFAULT '',
    content      TEXT        NOT NULL DEFAULT '',
    arguments    JSONB       NOT NULL DEFAULT '[]',
    category     TEXT        NOT NULL DEFAULT '',
    scope        TEXT        NOT NULL DEFAULT 'personal'
                             CHECK (scope IN ('global', 'persona', 'personal')),
    personas     TEXT[]      NOT NULL DEFAULT '{}',
    owner_email  TEXT        NOT NULL DEFAULT '',
    source       TEXT        NOT NULL DEFAULT 'operator'
                             CHECK (source IN ('operator', 'agent', 'system')),
    enabled      BOOLEAN     NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_prompts_scope   ON prompts(scope);
CREATE INDEX IF NOT EXISTS idx_prompts_owner   ON prompts(owner_email);
CREATE INDEX IF NOT EXISTS idx_prompts_enabled ON prompts(enabled);
