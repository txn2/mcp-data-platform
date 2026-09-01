-- The producer rows this migration wrote cannot be told apart from the ones
-- 000135 wrote for the same assets, so the asset backfill is deliberately not
-- undone: a row saying which script created an asset is true whichever
-- migration recorded it, and deleting it would strip 000135's work too.
DELETE FROM content_producers WHERE target_kind = 'collection';

ALTER TABLE content_producers DROP CONSTRAINT IF EXISTS content_producers_target_kind_check;
ALTER TABLE content_producers ADD CONSTRAINT content_producers_target_kind_check
    CHECK (target_kind IN ('asset', 'resource'));
