-- Restore the pre-backfill created_by from the stashed legacy value and
-- remove the stash. Only rows the up migration touched carry
-- metadata.legacy_created_by, so this reverses exactly that set.
UPDATE memory_records
SET created_by = metadata->>'legacy_created_by',
    metadata = metadata - 'legacy_created_by'
WHERE metadata ? 'legacy_created_by';
