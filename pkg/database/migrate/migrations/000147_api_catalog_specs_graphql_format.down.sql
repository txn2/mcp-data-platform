-- Reverse 000147. A graphql spec entry cannot be represented once the check
-- is narrowed, so the rows are dropped rather than left to fail the
-- constraint. A graphql connection that referenced one falls back to reading
-- its schema from its endpoint, which is what it did before #1745.
DELETE FROM api_catalog_specs WHERE spec_format = 'graphql';

ALTER TABLE api_catalog_specs
    DROP CONSTRAINT IF EXISTS api_catalog_specs_spec_format_check;

ALTER TABLE api_catalog_specs
    ADD CONSTRAINT api_catalog_specs_spec_format_check
        CHECK (spec_format IN ('openapi', 'wsdl'));
