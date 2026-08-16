-- 000104: the ordering substrate for the asset and collection lists (#1295).
--
-- Both lists now default to "most recently touched" — updated_at DESC with the
-- primary key as tie-breaker — rather than created_at DESC, and both are read
-- one owner at a time. These composite indexes match that access path exactly,
-- so the first page of a library is an index scan rather than a sort of every
-- row the owner has.
--
-- Both are partial on `deleted_at IS NULL` because every list query carries
-- that predicate: soft-deleted rows are never ordered, so they do not belong in
-- the index either.
--
-- The tie-breaker column is part of the index. Neither timestamp is unique, and
-- with LIMIT/OFFSET pagination a non-unique ordering lets a row straddling a
-- page boundary appear twice or not at all; including id makes the order total
-- and keeps the whole ORDER BY satisfiable from the index.
--
-- Only the default ordering is indexed. Sorting by name or size_bytes is a
-- deliberate, occasional choice over a per-owner library, and Postgres sorting
-- one owner's rows for it is cheaper than carrying two more indexes through
-- every write.
CREATE INDEX IF NOT EXISTS idx_portal_assets_owner_updated
    ON portal_assets (owner_id, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_portal_collections_owner_updated
    ON portal_collections (owner_id, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;
