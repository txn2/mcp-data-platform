-- Restore the pre-#1029 tool names in persona tool lists.
UPDATE persona_definitions
SET tools_allow = (
        SELECT COALESCE(jsonb_agg(
            CASE elem::text
                WHEN '"save_asset"' THEN '"save_artifact"'::jsonb
                WHEN '"manage_asset"' THEN '"manage_artifact"'::jsonb
                ELSE elem
            END
            ORDER BY ord
        ), '[]'::jsonb)
        FROM jsonb_array_elements(tools_allow) WITH ORDINALITY AS t(elem, ord)
    )
WHERE tools_allow @> '["save_asset"]'::jsonb
   OR tools_allow @> '["manage_asset"]'::jsonb;

UPDATE persona_definitions
SET tools_deny = (
        SELECT COALESCE(jsonb_agg(
            CASE elem::text
                WHEN '"save_asset"' THEN '"save_artifact"'::jsonb
                WHEN '"manage_asset"' THEN '"manage_artifact"'::jsonb
                ELSE elem
            END
            ORDER BY ord
        ), '[]'::jsonb)
        FROM jsonb_array_elements(tools_deny) WITH ORDINALITY AS t(elem, ord)
    )
WHERE tools_deny @> '["save_asset"]'::jsonb
   OR tools_deny @> '["manage_asset"]'::jsonb;
