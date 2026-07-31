-- Index the insight review status so the organization-wide insight search can
-- restrict to applied insights in SQL rather than after the top-k cut (#980 B2).
--
-- The review status is not a column: it lives in the insight overlay metadata
-- (insight_status, falling back to legacy_status for rows migrated from
-- knowledge_insights by migration 000031), because the status column is coarser
-- and stores pending, approved and applied alike as 'active'. Cross-owner search
-- must therefore filter on the expression, and an unindexed expression over
-- every owner's knowledge records is a sequential scan on the hot search path.
--
-- The expression MUST stay byte-identical to insightStatusExpr in
-- pkg/memory/postgres.go or the planner will not use this index. Partial on the
-- knowledge dimension because only knowledge-dimension records carry the
-- overlay, which keeps the index to the insight subset of the table.
CREATE INDEX IF NOT EXISTS idx_memory_records_insight_status
    ON memory_records ((COALESCE(NULLIF(metadata->>'insight_status', ''), NULLIF(metadata->>'legacy_status', ''))))
    WHERE dimension = 'knowledge';
