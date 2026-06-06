-- Prompt promotion request signal (Phase 2, #555).
--
-- An owner can request promotion of a personal prompt to a persona or global
-- scope. These columns record the pending request that the admin review queue
-- acts on. On approval the requested scope/personas are applied to the prompt,
-- status becomes 'approved', and the request flags are cleared. The lifecycle
-- state machine itself is unchanged (no new status); promotion-requested is an
-- orthogonal signal on a draft prompt.

ALTER TABLE prompts
    ADD COLUMN IF NOT EXISTS review_requested BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS requested_scope TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS requested_personas TEXT[] NOT NULL DEFAULT '{}';

-- Partial index backing the admin review queue (only pending requests).
CREATE INDEX IF NOT EXISTS idx_prompts_review_requested
    ON prompts (review_requested) WHERE review_requested;
