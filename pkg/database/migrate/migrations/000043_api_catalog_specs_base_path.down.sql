-- 000043 down: drop the per-spec base_path override column.
ALTER TABLE api_catalog_specs
    DROP COLUMN IF EXISTS base_path;
