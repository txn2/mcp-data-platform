-- Reverse 000060.

DROP INDEX IF EXISTS idx_prompts_review_requested;

ALTER TABLE prompts
    DROP COLUMN IF EXISTS requested_personas,
    DROP COLUMN IF EXISTS requested_scope,
    DROP COLUMN IF EXISTS review_requested;
