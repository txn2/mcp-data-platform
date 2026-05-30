-- 000049 down: drop the per-spec title/description override columns.
ALTER TABLE api_catalog_specs
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS title;
