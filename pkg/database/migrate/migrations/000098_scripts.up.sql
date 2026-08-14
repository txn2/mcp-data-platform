-- 000098: managed scripts — the script domain and its version history (#1283).
--
-- A managed script is agent-authored Starlark that the platform stores,
-- versions, and governs. The shape deliberately copies prompt_versions
-- (000085): a live row carrying the served state, plus an immutable snapshot
-- per mutation with the author and an approval stamp bound to that snapshot.
--
-- One column has no analogue in prompts and is the reason the two domains are
-- not merged: scripts.approved_version_id. The prompt review gate protects the
-- SUBSTANCE of a shared prompt — a draft prompt still serves to its owner. A
-- script is executed by the platform, so it needs the stronger invariant that
-- nothing executes unless the version being executed was approved.
-- approved_version_id is that gate: it is nullable, and the runner that
-- consumes it loads code from this column and nothing else. No approval action
-- exists yet — #1283 delivers the domain, the engine, and the authoring loop,
-- and every draft executes only through the owner-identity run_draft path,
-- which never reads this column. The action that sets it, and the run queue
-- that reads it, arrive with the execution gate in #1284; until then the column
-- is read-only and surfaced by manage_script get/list so an author can see that
-- a script is not yet executable.
--
-- Name uniqueness follows the prompt rule: a shared (global or persona) name is
-- unique platform-wide, while a personal name is unique only within its owner,
-- so two analysts may each keep their own "daily-sales" script.

CREATE TABLE IF NOT EXISTS scripts (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT        NOT NULL,
    display_name        TEXT        NOT NULL DEFAULT '',
    description         TEXT        NOT NULL DEFAULT '',
    source_code         TEXT        NOT NULL DEFAULT '',
    params              JSONB       NOT NULL DEFAULT '[]',
    scope               TEXT        NOT NULL DEFAULT 'personal'
                                    CHECK (scope IN ('global', 'persona', 'personal')),
    personas            TEXT[]      NOT NULL DEFAULT '{}',
    owner_email         TEXT        NOT NULL DEFAULT '',
    tags                TEXT[]      NOT NULL DEFAULT '{}',
    enabled             BOOLEAN     NOT NULL DEFAULT true,
    status              TEXT        NOT NULL DEFAULT 'draft'
                                    CHECK (status IN ('draft', 'active', 'deprecated', 'superseded')),
    superseded_by       TEXT        NOT NULL DEFAULT '',
    deprecated_at       TIMESTAMPTZ,
    version             INTEGER     NOT NULL DEFAULT 1,
    approved_version_id UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS script_versions (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    script_id    UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    version      INTEGER     NOT NULL,
    display_name TEXT        NOT NULL DEFAULT '',
    description  TEXT        NOT NULL DEFAULT '',
    source_code  TEXT        NOT NULL DEFAULT '',
    params       JSONB       NOT NULL DEFAULT '[]',
    tags         TEXT[]      NOT NULL DEFAULT '{}',
    author       TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL DEFAULT 'applied'
                             CHECK (status IN ('draft', 'applied', 'superseded', 'rejected')),
    approved_by  TEXT        NOT NULL DEFAULT '',
    approved_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (script_id, version)
);

-- The execution-gate pointer is added after script_versions exists, since the
-- two tables reference each other. ON DELETE RESTRICT: a version a script is
-- executing must not disappear out from under the runner, and version retention
-- is explicitly "anything a run, an approval, or a schedule references is kept".
-- Postgres has no ADD CONSTRAINT IF NOT EXISTS, so the guard is explicit,
-- keeping this migration re-runnable like every other statement here.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'scripts_approved_version_fk'
    ) THEN
        ALTER TABLE scripts
            ADD CONSTRAINT scripts_approved_version_fk
            FOREIGN KEY (approved_version_id) REFERENCES script_versions(id) ON DELETE RESTRICT;
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_name_shared
    ON scripts(name) WHERE scope <> 'personal';
CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_name_personal
    ON scripts(owner_email, name) WHERE scope = 'personal';

CREATE INDEX IF NOT EXISTS idx_scripts_scope    ON scripts(scope);
CREATE INDEX IF NOT EXISTS idx_scripts_owner    ON scripts(owner_email);
CREATE INDEX IF NOT EXISTS idx_scripts_enabled  ON scripts(enabled);
CREATE INDEX IF NOT EXISTS idx_scripts_personas ON scripts USING GIN(personas);
CREATE INDEX IF NOT EXISTS idx_script_versions_script ON script_versions(script_id);
