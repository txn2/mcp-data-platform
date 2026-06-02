-- 000058: allow 'embedded' as an api_catalog_specs source_kind
--
-- The built-in platform-admin self-connection seeds its catalog from the
-- OpenAPI document embedded in the binary (see internal/apidocs.SwaggerJSON
-- and pkg/platform/admin_self_connection.go). That seeded spec is written
-- with source_kind = 'embedded' so it is distinguishable from the
-- operator-managed inline/upload/url specs the admin API accepts.
--
-- Migration 000042 created the source_kind CHECK as an inline column
-- constraint, which Postgres auto-named api_catalog_specs_source_kind_check.
-- Drop that constraint and re-add it under the same name with 'embedded'
-- included. DROP ... IF EXISTS keeps this resilient if the auto-generated
-- name ever differed on an older deployment.
ALTER TABLE api_catalog_specs
    DROP CONSTRAINT IF EXISTS api_catalog_specs_source_kind_check;

ALTER TABLE api_catalog_specs
    ADD CONSTRAINT api_catalog_specs_source_kind_check
        CHECK (source_kind IN ('inline', 'upload', 'url', 'embedded'));
