ALTER TABLE knowledge_insights
    DROP COLUMN IF EXISTS changeset_ref,
    DROP COLUMN IF EXISTS applied_at,
    DROP COLUMN IF EXISTS applied_by;

DROP TABLE IF EXISTS knowledge_changesets;
