-- Reverse 000146. Dropping spec_format loses the distinction between an
-- OpenAPI spec and a rendered WSDL, and dropping openapi_content loses the
-- rendered document; a wsdl row's `content` is then a WSDL the gateway cannot
-- parse, and the connection registers with zero operations rather than
-- failing. That is the same state such a row had before the column existed.
ALTER TABLE api_catalog_specs
    DROP CONSTRAINT IF EXISTS api_catalog_specs_spec_format_check;

ALTER TABLE api_catalog_specs
    DROP COLUMN IF EXISTS openapi_content,
    DROP COLUMN IF EXISTS spec_format;
