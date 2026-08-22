-- Reverse 000120. Every per-asset retention override anybody set is discarded,
-- which is what reversing this migration means; version history itself is
-- untouched, and with the column gone nothing prunes it again.
ALTER TABLE portal_assets
    DROP CONSTRAINT IF EXISTS portal_assets_max_versions_nonneg;

ALTER TABLE portal_assets
    DROP COLUMN IF EXISTS max_versions;
