-- #1029 renamed the portal tools save_artifact -> save_asset and
-- manage_artifact -> manage_asset. Persona tool lists are glob patterns matched
-- against live tool names, so a DB-backed persona still naming the old tools
-- would silently change behaviour: an allow entry stops granting the tool, and
-- (worse) a deny entry stops denying it, failing open.
--
-- Rewrite the exact old names in place. Glob entries that merely happen to
-- match (for example "save_*") are untouched because they still match the new
-- names. Ordering within each array is preserved.
UPDATE persona_definitions
SET tools_allow = (
        SELECT COALESCE(jsonb_agg(
            CASE elem::text
                WHEN '"save_artifact"' THEN '"save_asset"'::jsonb
                WHEN '"manage_artifact"' THEN '"manage_asset"'::jsonb
                ELSE elem
            END
            ORDER BY ord
        ), '[]'::jsonb)
        FROM jsonb_array_elements(tools_allow) WITH ORDINALITY AS t(elem, ord)
    )
WHERE tools_allow @> '["save_artifact"]'::jsonb
   OR tools_allow @> '["manage_artifact"]'::jsonb;

UPDATE persona_definitions
SET tools_deny = (
        SELECT COALESCE(jsonb_agg(
            CASE elem::text
                WHEN '"save_artifact"' THEN '"save_asset"'::jsonb
                WHEN '"manage_artifact"' THEN '"manage_asset"'::jsonb
                ELSE elem
            END
            ORDER BY ord
        ), '[]'::jsonb)
        FROM jsonb_array_elements(tools_deny) WITH ORDINALITY AS t(elem, ord)
    )
WHERE tools_deny @> '["save_artifact"]'::jsonb
   OR tools_deny @> '["manage_artifact"]'::jsonb;
