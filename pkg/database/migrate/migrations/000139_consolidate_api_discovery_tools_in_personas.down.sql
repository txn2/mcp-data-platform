-- Restore the pre-#1592 API discovery tool names in persona tool lists. The
-- consolidated name expands to the three tools it stood for, in place, so a
-- persona that held api_discover holds all three again; which of the three it
-- originally named is not recorded, so the widest reading is restored.
CREATE OR REPLACE FUNCTION pg_temp.expand_api_discovery_tools(entries JSONB) RETURNS JSONB AS $$
    SELECT COALESCE(jsonb_agg(expanded ORDER BY ord, sub), '[]'::jsonb)
    FROM (
        SELECT
            CASE elem
                WHEN 'api_discover' THEN '["api_list_specs", "api_list_endpoints", "api_get_endpoint_schema"]'::jsonb
                ELSE jsonb_build_array(elem)
            END AS group_elems,
            ord
        FROM jsonb_array_elements_text(entries) WITH ORDINALITY AS t(elem, ord)
    ) grouped,
    LATERAL jsonb_array_elements(grouped.group_elems) WITH ORDINALITY AS g(expanded, sub)
$$ LANGUAGE SQL;

UPDATE persona_definitions
SET tools_allow = pg_temp.expand_api_discovery_tools(tools_allow)
WHERE tools_allow @> '["api_discover"]'::jsonb;

UPDATE persona_definitions
SET tools_deny = pg_temp.expand_api_discovery_tools(tools_deny)
WHERE tools_deny @> '["api_discover"]'::jsonb;

DROP FUNCTION pg_temp.expand_api_discovery_tools(JSONB);
