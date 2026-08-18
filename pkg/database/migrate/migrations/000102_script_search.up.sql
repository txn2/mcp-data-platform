-- 000102: managed-script discovery — the lexical index behind the scripts
-- search source (#1302).
--
-- A script is the only first-class entity the platform holds that search cannot
-- find. This migration adds the ranking substrate for the scripts provider:
-- a full-text document composed from what a caller searches a script BY — its
-- title, its description, its tags, and its parameter contract.
--
-- The source code is deliberately not in the document. Discovery answers "what
-- is this and can I use it"; reading the Starlark is what manage_script get is
-- for, and what a reviewer does. Indexing it would rank a script by identifiers
-- and SQL fragments that describe nothing about its purpose.
--
-- There is no embedding column and no vector index. Scripts are ranked
-- lexically, like feedback threads: the corpus is small, its text is short and
-- deliberate (a name, a sentence, a parameter list), and an embedding pipeline
-- for it would be machinery without a matching gain.
--
-- script_fts is wrapped rather than inlined for the same reason prompt_fts is
-- (000062): array_to_string and the parameter extraction are composed into a
-- GIN index expression, which requires every function in it be IMMUTABLE. The
-- composition is deterministic, so marking the wrapper IMMUTABLE is correct.
-- The request-path search must call script_fts with this exact argument order
-- to hit the index.
--
-- script_fts calls nothing but built-ins, which is a correctness requirement
-- and not a style choice. PostgreSQL 17 runs maintenance operations, CREATE
-- INDEX among them, with search_path restricted to pg_catalog and pg_temp. A
-- call to a function of ours from inside a body that the planner inlines to
-- build the index is resolved under that restricted path and is not found, so
-- the index build fails with "function ... does not exist". Built-ins live in
-- pg_catalog and resolve either way. Every other _fts function in this schema
-- (prompt_fts, portal_asset_fts, portal_collection_fts, resource_fts,
-- catalog_dataset_fts, portal_knowledge_page_fts) already holds to this;
-- script_fts is the one that did not, and 000111 converges the deployments
-- that applied the earlier shape on PostgreSQL 16, where it was accepted.
--
-- The parameter arm flattens the typed contract to the text worth matching:
-- each parameter's name and description. The type, the default, and the enum
-- values are contract mechanics, not language anyone searches by.
--
-- The jsonb_typeof guard keeps the index expression total. params is declared
-- NOT NULL DEFAULT '[]' and every writer sends an array, but jsonb_array_elements
-- raises on any other shape, and an index expression that can raise makes the
-- row unwritable rather than merely unfindable.
--
-- The title is coalesce(nullif(display_name,''), name) so a script with no
-- display name is still findable by the name an agent would call it by.
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
