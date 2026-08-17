-- Return already-rolled-back insights to the review queue (#1257).
--
-- rolled_back was a terminal insight status with no edge back into the queue, so
-- rolling back a promotion reverted the change and stranded the insight that
-- motivated it: not live, not pending, and counted by nothing. The status is gone
-- from the lifecycle; a rollback now returns its source insights to pending, the
-- one state the review queue is made of. Rows stranded before this upgrade are
-- returned here so an existing deployment's queue is the complete picture of
-- outstanding work rather than one missing every rolled-back item.
--
-- applied_by, applied_at and changeset_ref stay in the metadata: they are the
-- record of the application that was reverted, which is what the next reviewer
-- needs to avoid re-deciding blind. The status column goes back to active because
-- a pending insight is an active record, exactly as it was before it was applied.
--
-- The status expression MUST stay byte-identical to insightStatusExpr in
-- pkg/memory/postgres.go (and migration 000095's index), and the review note MUST
-- stay identical to RollbackReviewNote in pkg/toolkits/knowledge/rollback.go,
-- which writes the same sentence on the runtime path.
UPDATE memory_records
SET metadata = metadata || jsonb_build_object(
        'insight_status', 'pending',
        'review_notes', CASE
            WHEN COALESCE(metadata->>'changeset_ref', '') = ''
                THEN 'Returned to review: the changeset that applied this insight was rolled back.'
            ELSE 'Returned to review: changeset ' || (metadata->>'changeset_ref') || ' was rolled back.'
        END
    ),
    status = 'active',
    updated_at = NOW()
WHERE dimension = 'knowledge'
  AND COALESCE(NULLIF(metadata->>'insight_status', ''), NULLIF(metadata->>'legacy_status', '')) = 'rolled_back';
