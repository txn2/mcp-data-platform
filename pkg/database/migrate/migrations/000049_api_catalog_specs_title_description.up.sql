-- 000049: api_catalog_specs.title / api_catalog_specs.description
--
-- Operator-overridable per-spec display metadata. api_list_specs and
-- the multi-spec gate on api_list_endpoints emit one summary row per
-- component spec so an agent can pick a section before it drills into
-- the operations. Each summary carries a title and description.
--
-- When empty (the default), the toolkit derives both values from the
-- spec content's info.title / info.description at registration time.
-- The explicit override exists for two cases the derivation does not
-- handle:
--   1. Specs that ship with an empty or unhelpful info.description,
--      where deriving yields a blank or generic label.
--   2. Specs where the operator wants a deployment-specific label
--      (the same spec mounted under two connections with different
--      operator-facing names).
--
-- TEXT columns. Validation (trim, no CR/LF/NUL, length caps of 200
-- for title and 2000 for description) happens in the catalog Go layer
-- at write time. Empty string is the "no override" sentinel. Both
-- columns are NOT NULL with DEFAULT '', so rows that existed before
-- this migration backfill to '' and the toolkit's empty-string check
-- covers pre-migration and new never-set values uniformly.

ALTER TABLE api_catalog_specs
    ADD COLUMN title       TEXT NOT NULL DEFAULT '',
    ADD COLUMN description TEXT NOT NULL DEFAULT '';
