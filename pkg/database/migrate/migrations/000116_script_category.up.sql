-- 000116: a script's category, and the ranked search that reads it (#1369).
--
-- A script is a document as much as it is code, and until now the record
-- carried only one classification axis: tags. Every sibling library the
-- platform holds carries two -- resources and insights file a record under a
-- CATEGORY (one lowercase slug, filterable, part of the search field list) and
-- tag it besides -- and scripts were the exception. A listing with no category
-- axis is a flat list of every automation anybody owns.
--
-- The column is added to BOTH tables. scripts carries the live value and
-- script_versions carries it in the snapshot, because the category is one of
-- the fields SnapshotChanged versions: the four fields that document a script
-- (display name, description, category, tags) are edited from one form and
-- captured as one version, and a category that lived only on the live row would
-- be the one field a version could not explain.
--
-- The default is '' rather than NULL. Every existing script is uncategorized,
-- an empty category is a legitimate resting state (most scripts are never
-- filed), and NOT NULL DEFAULT '' keeps the FTS expression below total without
-- a coalesce that would have to be right in three places.
ALTER TABLE scripts
    ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';

ALTER TABLE script_versions
    ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';

-- The listing filters on the category, so the column is indexed for it. The
-- partial predicate keeps the index to the rows a filter can ever match: an
-- uncategorized script is never the answer to "show me the category X scripts",
-- and on a corpus where most rows are uncategorized that is most of the table.
CREATE INDEX IF NOT EXISTS idx_scripts_category
    ON scripts (category) WHERE category <> '';

-- script_fts gains the category, which means the function and the GIN index
-- built on it move together, in one migration: changing the expression without
-- rebuilding the index would silently leave the planner with an index it can no
-- longer match, and a sequential scan behind it.
--
-- The five-argument function is deliberately NOT dropped. Migrations run at
-- process start, so during a rolling deployment the new replica applies this
-- while replicas running the previous image are still serving, and every one of
-- their script searches calls script_fts(display_name, name, description, tags,
-- params). Dropping it would answer each of those with "function does not
-- exist" until the rollout finished. The two arities are distinct overloads and
-- a five-argument call resolves unambiguously, so leaving the old one costs
-- nothing but a dead function; a deployment that wants it gone can drop it once
-- no replica calls it. The old INDEX is dropped, which those replicas survive:
-- their query still answers, on a sequential scan, for the length of a rollout.
--
-- The argument order is identity, then prose, then the two classification axes,
-- then the parameter contract. internal/platform/scriptstore/search.go calls it
-- with exactly this order; the two must be changed together or the index is
-- dropped from the plan.
--
-- Everything else about the function is unchanged and unchanged deliberately.
-- It calls nothing but built-ins, for the reason 000102 and 000111 record:
-- PostgreSQL 17 builds an index with search_path restricted to pg_catalog, so a
-- call to a function of ours from inside an inlined body is not found and the
-- index build fails. And the source code is still absent from the document --
-- discovery answers "what is this and can I use it", and the source is admitted
-- to a narrower audience than the contract is.
DROP INDEX IF EXISTS idx_scripts_search_fts;

CREATE OR REPLACE FUNCTION script_fts(
    display_name text, name text, description text, category text, tags text[], params jsonb
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(nullif(display_name, ''), name) || ' ' ||
        coalesce(description, '')                || ' ' ||
        coalesce(category, '')                   || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        CASE WHEN jsonb_typeof(params) = 'array' THEN
            coalesce((
                SELECT string_agg(
                    coalesce(p->>'name', '') || ' ' || coalesce(p->>'description', ''), ' ')
                FROM jsonb_array_elements(params) AS p), '')
        ELSE '' END);
$$;

CREATE INDEX IF NOT EXISTS idx_scripts_search_fts
    ON scripts USING gin (script_fts(display_name, name, description, category, tags, params));
