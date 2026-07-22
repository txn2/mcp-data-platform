-- Prompt collections (#1010): named groups organizing the prompt library by
-- team, domain, or workflow. Collections are org-visible entities; a prompt
-- belongs to at most one collection (prompts.collection_id), and uncollected
-- prompts list under a default group in the portal. Collections replace
-- free-text category as the primary organizing structure; category remains a
-- legacy filter until content migrates.

CREATE TABLE IF NOT EXISTS prompt_collections (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    created_by  TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Collection names are an org-wide vocabulary; case-insensitive uniqueness
-- prevents "Sales" and "sales" coexisting as distinct groups.
CREATE UNIQUE INDEX IF NOT EXISTS uq_prompt_collections_name
    ON prompt_collections (LOWER(name));

-- At-most-one collection per prompt. Deleting a collection releases its
-- prompts to the default (uncollected) group rather than deleting them.
ALTER TABLE prompts ADD COLUMN IF NOT EXISTS collection_id UUID
    REFERENCES prompt_collections(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_prompts_collection
    ON prompts (collection_id) WHERE collection_id IS NOT NULL;
