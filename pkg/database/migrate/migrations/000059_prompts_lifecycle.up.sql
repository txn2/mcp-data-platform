-- Prompt lifecycle + per-owner naming.
--
-- Adds the promotion/lifecycle state (status, tags, approval and supersede
-- metadata, and the review-request signal) and changes name uniqueness from
-- global to scope-aware: global and persona prompts are registered on the
-- shared MCP server by name, so their names stay globally unique; personal
-- prompts are served per-owner, so their names are unique only within an owner.

ALTER TABLE prompts
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'approved', 'deprecated', 'superseded')),
    ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS approved_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deprecated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS superseded_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS review_requested BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS requested_scope TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS requested_personas TEXT[] NOT NULL DEFAULT '{}';

-- Existing prompts are already live and served, so treat them as approved;
-- only newly created prompts start as draft.
UPDATE prompts
   SET status = 'approved',
       approved_at = COALESCE(approved_at, updated_at)
 WHERE status = 'draft';

-- Replace the global unique(name) with scope-aware uniqueness.
ALTER TABLE prompts DROP CONSTRAINT IF EXISTS prompts_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS uq_prompts_name_shared
    ON prompts (name) WHERE scope <> 'personal';

CREATE UNIQUE INDEX IF NOT EXISTS uq_prompts_name_personal
    ON prompts (owner_email, name) WHERE scope = 'personal';

CREATE INDEX IF NOT EXISTS idx_prompts_status ON prompts (status);
CREATE INDEX IF NOT EXISTS idx_prompts_tags   ON prompts USING GIN (tags);
