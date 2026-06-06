-- Reverse 000059. Restoring the global unique(name) assumes no two personal
-- prompts share a name across owners; if such rows exist (created under the
-- per-owner scheme) they must be reconciled before downgrading.

DROP INDEX IF EXISTS idx_prompts_tags;
DROP INDEX IF EXISTS idx_prompts_status;
DROP INDEX IF EXISTS uq_prompts_name_personal;
DROP INDEX IF EXISTS uq_prompts_name_shared;

ALTER TABLE prompts ADD CONSTRAINT prompts_name_key UNIQUE (name);

ALTER TABLE prompts
    DROP COLUMN IF EXISTS superseded_by,
    DROP COLUMN IF EXISTS deprecated_at,
    DROP COLUMN IF EXISTS approved_at,
    DROP COLUMN IF EXISTS approved_by,
    DROP COLUMN IF EXISTS tags,
    DROP COLUMN IF EXISTS status;
