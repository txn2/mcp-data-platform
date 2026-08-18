-- 000111: converge the scripts lexical index onto a self-contained script_fts.
--
-- 000102 originally composed script_fts from a second function of ours,
-- script_param_text, and built the GIN index over the composition. PostgreSQL
-- 16 accepts that; PostgreSQL 17 does not. Since 17, maintenance operations —
-- CREATE INDEX and REINDEX among them — run with search_path restricted to
-- pg_catalog and pg_temp, and the inner call is resolved under that restricted
-- path while the planner inlines the body to build the index. It is not found,
-- and the build fails with "function script_param_text(jsonb) does not exist".
--
-- 000102 now carries the self-contained definition, which is what a fresh
-- install gets. This migration exists for the deployments that applied the
-- earlier shape on PostgreSQL 16 and are therefore already past 102. Nothing
-- is broken for them today: the restriction applies to index builds, not to
-- reads, so their searches work. What they cannot do is rebuild that index —
-- a REINDEX, or a major-version upgrade to 17 that reindexes — which would
-- fail on an index they already depend on, at the least convenient moment.
--
-- Idempotent by construction, so a fresh install that just ran the corrected
-- 000102 passes through it having changed nothing that matters.

-- The index is dropped before the function is replaced. Replacing a function an
-- index expression depends on is not refused by PostgreSQL and is the standard
-- way to silently invalidate that index, so the dependency is removed first and
-- the index is rebuilt from the new definition below.
DROP INDEX IF EXISTS idx_scripts_search_fts;

-- Identical to the definition 000102 now carries. Stated in full rather than
-- referenced, because a migration has to be readable as the thing it applies.
CREATE OR REPLACE FUNCTION script_fts(
    display_name text, name text, description text, tags text[], params jsonb
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(nullif(display_name, ''), name) || ' ' ||
        coalesce(description, '')                || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        CASE WHEN jsonb_typeof(params) = 'array' THEN
            coalesce((
                SELECT string_agg(
                    coalesce(p->>'name', '') || ' ' || coalesce(p->>'description', ''), ' ')
                FROM jsonb_array_elements(params) AS p), '')
        ELSE '' END);
$$;

CREATE INDEX IF NOT EXISTS idx_scripts_search_fts
    ON scripts USING gin (script_fts(display_name, name, description, tags, params));

-- Nothing references the helper once script_fts no longer does.
DROP FUNCTION IF EXISTS script_param_text(jsonb);
