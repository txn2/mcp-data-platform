-- #1592 folded the three API discovery tools into one: api_list_specs,
-- api_list_endpoints and api_get_endpoint_schema are api_discover, which
-- answers at the depth its arguments select. Persona tool lists are glob
-- patterns matched against live tool names, so a DB-backed persona still
-- naming an old tool would silently change behaviour: an allow entry stops
-- granting anything, and a deny entry stops denying anything, failing open.
--
-- Rewrite the exact old names, and the verb globs that could only ever have
-- matched them (api_list_*, api_get_*), in place; api_list_* would otherwise
-- stop matching, since api_discover has no such prefix. Broader globs (api_*,
-- *_list_*) are untouched. An entry the rewrite makes a duplicate is kept
-- once, at its first position, so the order of everything else is preserved.
--
-- A deny of any one of the three becomes a deny of api_discover, which
-- withholds the other two depths too: the migration fails closed. The three
-- were one read path at three depths, so no persona is known to have granted
-- one and denied another.
CREATE OR REPLACE FUNCTION pg_temp.consolidate_api_discovery_tools(entries JSONB) RETURNS JSONB AS $$
    SELECT COALESCE(jsonb_agg(elem ORDER BY ord), '[]'::jsonb)
    FROM (
        SELECT elem, MIN(ord) AS ord
        FROM (
            SELECT
                CASE elem
                    WHEN 'api_list_specs' THEN 'api_discover'
                    WHEN 'api_list_endpoints' THEN 'api_discover'
                    WHEN 'api_list_*' THEN 'api_discover'
                    WHEN 'api_get_endpoint_schema' THEN 'api_discover'
                    WHEN 'api_get_*' THEN 'api_discover'
                    ELSE elem
                END AS elem,
                ord
            FROM jsonb_array_elements_text(entries) WITH ORDINALITY AS t(elem, ord)
        ) mapped
        GROUP BY elem
    ) deduped
$$ LANGUAGE SQL;

UPDATE persona_definitions
SET tools_allow = pg_temp.consolidate_api_discovery_tools(tools_allow)
WHERE EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(tools_allow) AS e
    WHERE e IN ('api_list_specs', 'api_list_endpoints', 'api_list_*',
                'api_get_endpoint_schema', 'api_get_*')
);

UPDATE persona_definitions
SET tools_deny = pg_temp.consolidate_api_discovery_tools(tools_deny)
WHERE EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(tools_deny) AS e
    WHERE e IN ('api_list_specs', 'api_list_endpoints', 'api_list_*',
                'api_get_endpoint_schema', 'api_get_*')
);

DROP FUNCTION pg_temp.consolidate_api_discovery_tools(JSONB);
