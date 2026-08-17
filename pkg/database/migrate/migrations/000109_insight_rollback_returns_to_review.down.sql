-- Send returned insights back to the terminal rolled_back status (#1257).
--
-- The review note the up migration (and the runtime rollback path) writes is what
-- identifies a returned insight, so this reverses exactly the rows that were
-- returned and leaves every other pending insight alone.
UPDATE memory_records
SET metadata = metadata || jsonb_build_object('insight_status', 'rolled_back'),
    status = 'archived',
    updated_at = NOW()
WHERE dimension = 'knowledge'
  AND COALESCE(NULLIF(metadata->>'insight_status', ''), NULLIF(metadata->>'legacy_status', '')) = 'pending'
  AND metadata->>'review_notes' LIKE 'Returned to review:%';
