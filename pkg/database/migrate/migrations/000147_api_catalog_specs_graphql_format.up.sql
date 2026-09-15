-- 000147: a catalog spec entry may hold a GraphQL schema
--
-- A graphql connection kept its schema per connection, keyed by connection
-- name, with no way to register one from a URL and no way for two connections
-- against one endpoint to share it (#1745). An OpenAPI endpoint in the same
-- position has a catalog: a named, versioned spec bundle, refreshed on etag
-- from a stable address and referenced by many connections at once.
--
-- Widening this check is the whole schema change. A graphql spec entry is an
-- SDL document in `content`, with `openapi_content` empty: unlike a WSDL, an
-- SDL is not rendered into OpenAPI, because it is not served to the HTTP
-- gateway at all -- the graphql toolkit reads it as the schema a connection
-- answers with. `operation_count`, `source_kind`, `source_url`, `etag` and
-- `last_fetched_at` mean for it exactly what they mean for an OpenAPI spec,
-- and the operation embeddings in api_catalog_operation_embeddings are keyed
-- on (catalog_id, spec_name, operation_id) as they already are, which is what
-- lets two connections on one catalog embed the schema once.
ALTER TABLE api_catalog_specs
    DROP CONSTRAINT IF EXISTS api_catalog_specs_spec_format_check;

ALTER TABLE api_catalog_specs
    ADD CONSTRAINT api_catalog_specs_spec_format_check
        CHECK (spec_format IN ('openapi', 'wsdl', 'graphql'));
