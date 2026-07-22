-- Prompt versioning and approval provenance (#1009).
--
-- prompt_versions snapshots the reviewable substance of a prompt (content,
-- display name, description, arguments, tags) on each mutation, with a
-- monotonically increasing per-prompt version number, the author, and the
-- approval stamp bound to that specific version. The live prompts row remains
-- the served state; prompts.version records which snapshot it carries. A
-- content edit to an approved shared prompt lands here as a 'draft' row and is
-- served only after an admin approves it (status 'applied').

CREATE TABLE IF NOT EXISTS prompt_versions (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    prompt_id    UUID        NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    version      INTEGER     NOT NULL,
    display_name TEXT        NOT NULL DEFAULT '',
    description  TEXT        NOT NULL DEFAULT '',
    content      TEXT        NOT NULL DEFAULT '',
    arguments    JSONB       NOT NULL DEFAULT '[]',
    tags         TEXT[]      NOT NULL DEFAULT '{}',
    author       TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL DEFAULT 'applied'
                             CHECK (status IN ('draft', 'applied', 'superseded', 'rejected')),
    approved_by  TEXT        NOT NULL DEFAULT '',
    approved_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (prompt_id, version)
);

CREATE INDEX IF NOT EXISTS idx_prompt_versions_prompt ON prompt_versions(prompt_id);

-- The live row carries the number of the snapshot it is serving.
ALTER TABLE prompts ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;

-- Existing prompts migrate as v1 with their current content; the approval
-- stamp carries over to that version. System rows (source='system') are
-- read-only mirrors of server configuration re-ingested at startup and are
-- never versioned.
INSERT INTO prompt_versions (prompt_id, version, display_name, description,
                             content, arguments, tags, author, status,
                             approved_by, approved_at, created_at)
SELECT id, 1, display_name, description, content, arguments, tags,
       owner_email, 'applied', approved_by, approved_at, updated_at
  FROM prompts
 WHERE source <> 'system'
ON CONFLICT (prompt_id, version) DO NOTHING;

-- Usage stats (#1009) aggregate prompt-serve audit events by prompt id. The
-- partial expression index keeps the run-count/last-run rollup off a full
-- partition scan.
CREATE INDEX IF NOT EXISTS idx_audit_logs_prompt_serve
    ON audit_logs ((parameters->>'prompt_id'))
    WHERE event_kind = 'prompt_serve';
