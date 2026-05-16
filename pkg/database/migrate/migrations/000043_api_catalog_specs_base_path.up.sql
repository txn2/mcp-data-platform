-- 000043: api_catalog_specs.base_path
--
-- Operator-overridable per-spec base path. When set, the toolkit
-- prepends this value to every operation path emitted in
-- api_list_endpoints and api_get_endpoint_schema output, AND uses
-- it at api_invoke_endpoint time. When empty (the default), the
-- toolkit falls back to deriving the base path from the spec's
-- servers[0].url and dedupes against the connection's base_url.
--
-- The explicit override is necessary for two real cases the
-- auto-derivation does not handle:
--   1. Specs that ship without a servers[] entry at all (any
--      generator can produce these), where the toolkit otherwise
--      has nothing to prepend.
--   2. Specs whose servers[0].url points at the vendor's own
--      production host but the operator is targeting a sandbox,
--      proxy, or version-pinned alias whose path differs.
--
-- TEXT with no length cap: an HTTP path can be arbitrarily long.
-- Validation (leading slash, no embedded control chars) happens in
-- the catalog Go layer at write time. Empty string is the "no
-- override" sentinel. The column is NOT NULL with DEFAULT '', so
-- rows that existed before this migration backfill to '' and the
-- toolkit's empty-string check covers both pre-migration and new
-- never-set values uniformly.

ALTER TABLE api_catalog_specs
    ADD COLUMN base_path TEXT NOT NULL DEFAULT '';
