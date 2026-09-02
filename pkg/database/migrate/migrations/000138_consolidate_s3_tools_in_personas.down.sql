-- Restore the pre-#1591 S3 tool names in persona tool lists. One consolidated
-- name expands to every tool it stood for, in place, so a persona that held
-- s3_object holds all six object tools again; which of the six it originally
-- named is not recorded, so the widest reading is restored.
CREATE OR REPLACE FUNCTION pg_temp.expand_s3_tools(entries JSONB) RETURNS JSONB AS $$
    SELECT COALESCE(jsonb_agg(expanded ORDER BY ord, sub), '[]'::jsonb)
    FROM (
        SELECT
            CASE elem
                WHEN 's3_list' THEN '["s3_list_buckets", "s3_list_objects"]'::jsonb
                WHEN 's3_object' THEN '["s3_get_object", "s3_get_object_metadata", "s3_presign_url", "s3_put_object", "s3_copy_object", "s3_delete_object"]'::jsonb
                ELSE jsonb_build_array(elem)
            END AS group_elems,
            ord
        FROM jsonb_array_elements_text(entries) WITH ORDINALITY AS t(elem, ord)
    ) grouped,
    LATERAL jsonb_array_elements(grouped.group_elems) WITH ORDINALITY AS g(expanded, sub)
$$ LANGUAGE SQL;

UPDATE persona_definitions
SET tools_allow = pg_temp.expand_s3_tools(tools_allow)
WHERE tools_allow @> '["s3_list"]'::jsonb OR tools_allow @> '["s3_object"]'::jsonb;

UPDATE persona_definitions
SET tools_deny = pg_temp.expand_s3_tools(tools_deny)
WHERE tools_deny @> '["s3_list"]'::jsonb OR tools_deny @> '["s3_object"]'::jsonb;

DROP FUNCTION pg_temp.expand_s3_tools(JSONB);
