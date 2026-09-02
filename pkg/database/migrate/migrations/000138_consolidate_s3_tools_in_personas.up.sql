-- #1591 consolidated the eight S3 tools into two: s3_list_buckets and
-- s3_list_objects are s3_list; s3_get_object, s3_get_object_metadata,
-- s3_presign_url, s3_put_object, s3_copy_object and s3_delete_object are
-- s3_object. Persona tool lists are glob patterns matched against live tool
-- names, so a DB-backed persona still naming an old tool would silently change
-- behaviour: an allow entry stops granting anything, and a deny entry stops
-- denying anything, failing open.
--
-- Rewrite the exact old names, and the verb globs that could only ever have
-- matched them (s3_list_*, s3_get_*, s3_put_*, s3_copy_*, s3_delete_*,
-- s3_presign_*), in place; s3_list_* would otherwise stop matching, since
-- s3_list has no suffix. Broader globs (s3_*, *_list_*) are untouched. An
-- entry the rewrite makes a duplicate is kept once, at its first position, so
-- the order of everything else is preserved.
--
-- A deny of a former write tool becomes a deny of s3_object, which also
-- withholds the read actions: the migration fails closed. Read-but-not-write
-- on S3 objects is expressed by the connection's read_only flag, not by a
-- persona's tool list.
CREATE OR REPLACE FUNCTION pg_temp.consolidate_s3_tools(entries JSONB) RETURNS JSONB AS $$
    SELECT COALESCE(jsonb_agg(elem ORDER BY ord), '[]'::jsonb)
    FROM (
        SELECT elem, MIN(ord) AS ord
        FROM (
            SELECT
                CASE elem
                    WHEN 's3_list_buckets' THEN 's3_list'
                    WHEN 's3_list_objects' THEN 's3_list'
                    WHEN 's3_list_*' THEN 's3_list'
                    WHEN 's3_get_object' THEN 's3_object'
                    WHEN 's3_get_object_metadata' THEN 's3_object'
                    WHEN 's3_get_*' THEN 's3_object'
                    WHEN 's3_presign_url' THEN 's3_object'
                    WHEN 's3_presign_*' THEN 's3_object'
                    WHEN 's3_put_object' THEN 's3_object'
                    WHEN 's3_put_*' THEN 's3_object'
                    WHEN 's3_copy_object' THEN 's3_object'
                    WHEN 's3_copy_*' THEN 's3_object'
                    WHEN 's3_delete_object' THEN 's3_object'
                    WHEN 's3_delete_*' THEN 's3_object'
                    ELSE elem
                END AS elem,
                ord
            FROM jsonb_array_elements_text(entries) WITH ORDINALITY AS t(elem, ord)
        ) mapped
        GROUP BY elem
    ) deduped
$$ LANGUAGE SQL;

UPDATE persona_definitions
SET tools_allow = pg_temp.consolidate_s3_tools(tools_allow)
WHERE EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(tools_allow) AS e
    WHERE e IN ('s3_list_buckets', 's3_list_objects', 's3_list_*',
                's3_get_object', 's3_get_object_metadata', 's3_get_*',
                's3_presign_url', 's3_presign_*', 's3_put_object', 's3_put_*',
                's3_copy_object', 's3_copy_*', 's3_delete_object', 's3_delete_*')
);

UPDATE persona_definitions
SET tools_deny = pg_temp.consolidate_s3_tools(tools_deny)
WHERE EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(tools_deny) AS e
    WHERE e IN ('s3_list_buckets', 's3_list_objects', 's3_list_*',
                's3_get_object', 's3_get_object_metadata', 's3_get_*',
                's3_presign_url', 's3_presign_*', 's3_put_object', 's3_put_*',
                's3_copy_object', 's3_copy_*', 's3_delete_object', 's3_delete_*')
);

DROP FUNCTION pg_temp.consolidate_s3_tools(JSONB);
