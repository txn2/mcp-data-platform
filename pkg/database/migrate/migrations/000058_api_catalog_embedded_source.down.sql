-- 000058 (down): restore the pre-embedded source_kind CHECK constraint.
--
-- Any rows written with source_kind = 'embedded' (the platform-admin
-- self-seed) would violate the restored constraint, so delete them first.
-- This is safe: the self-seed re-creates the spec on the next startup of a
-- binary that still supports it. Orphaned catalog header rows (if any) are
-- harmless and left in place.
DELETE FROM api_catalog_specs WHERE source_kind = 'embedded';

ALTER TABLE api_catalog_specs
    DROP CONSTRAINT IF EXISTS api_catalog_specs_source_kind_check;

ALTER TABLE api_catalog_specs
    ADD CONSTRAINT api_catalog_specs_source_kind_check
        CHECK (source_kind IN ('inline', 'upload', 'url'));
