-- 000160: folders are stored (#1872).
--
-- A folder used to be derived from the paths of the resources filed under it,
-- so it could not exist empty: "New folder" had nothing to write, and a folder
-- vanished the moment its last file moved out. The Resources page is a file
-- manager, and in a file manager a folder is a thing of its own.
--
-- A row is one folder of one library. scope_id is NULL for the global library,
-- as it is on resources, so the visibility predicate the listing runs applies
-- to this table unchanged; the unique index reads NULL as '' so the global
-- library holds each path once.
--
-- The folders are still also derived: a listing is the union of these rows and
-- the paths in use, so a writer that files a resource without recording its
-- folder still shows it. Every write path in the platform records the chain.
CREATE TABLE IF NOT EXISTS resource_folders (
    scope      TEXT        NOT NULL,
    scope_id   TEXT,
    path       TEXT        NOT NULL,
    created_by TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS resource_folders_library_path
    ON resource_folders (scope, (COALESCE(scope_id, '')), path);

-- Every folder in use today becomes a row, each ancestor included, so a folder
-- emptied after this migration keeps its place.
INSERT INTO resource_folders (scope, scope_id, path, created_at)
SELECT r.scope, r.scope_id, array_to_string(p.parts[1:i], '/'), MIN(r.created_at)
FROM resources r
CROSS JOIN LATERAL (SELECT string_to_array(r.path, '/') AS parts) AS p
CROSS JOIN LATERAL generate_subscripts(p.parts, 1) AS i
WHERE r.path <> ''
GROUP BY r.scope, r.scope_id, array_to_string(p.parts[1:i], '/')
ON CONFLICT DO NOTHING;
