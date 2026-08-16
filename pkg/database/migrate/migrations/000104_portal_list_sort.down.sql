-- Reverse 000104: drop the per-owner recency indexes. The lists still order by
-- updated_at without them; only the index scan is lost.
DROP INDEX IF EXISTS idx_portal_assets_owner_updated;
DROP INDEX IF EXISTS idx_portal_collections_owner_updated;
