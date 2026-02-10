ALTER TABLE knowledge_insights
    DROP COLUMN IF EXISTS review_notes,
    DROP COLUMN IF EXISTS reviewed_at,
    DROP COLUMN IF EXISTS reviewed_by;
