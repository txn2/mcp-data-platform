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
-- Both functions are wrapped rather than inlined for the same reason prompt_fts
-- is (000062): array_to_string and the parameter extraction are composed into a
-- GIN index expression, which requires every function in it be IMMUTABLE. The
-- composition is deterministic, so marking the wrappers IMMUTABLE is correct.
-- The request-path search must call script_fts with this exact argument order
-- to hit the index.

-- script_param_text flattens the typed parameter contract to the text worth
-- matching: each parameter's name and description. The type, the default, and
-- the enum values are contract mechanics, not language anyone searches by.
--
-- The jsonb_typeof guard keeps the index expression total. params is declared
-- NOT NULL DEFAULT '[]' and every writer sends an array, but jsonb_array_elements
-- raises on any other shape, and an index expression that can raise makes the
-- row unwritable rather than merely unfindable.
CREATE OR REPLACE FUNCTION script_param_text(params jsonb)
RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE WHEN jsonb_typeof(params) = 'array' THEN
        coalesce((
            SELECT string_agg(
                coalesce(p->>'name', '') || ' ' || coalesce(p->>'description', ''), ' ')
            FROM jsonb_array_elements(params) AS p), '')
    ELSE '' END;
$$;

-- script_fts composes the lexical document. The title is
-- coalesce(nullif(display_name,''), name) so a script with no display name is
-- still findable by the name an agent would call it by.
CREATE OR REPLACE FUNCTION script_fts(
    display_name text, name text, description text, tags text[], params jsonb
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(nullif(display_name, ''), name) || ' ' ||
        coalesce(description, '')                || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        script_param_text(params));
$$;

CREATE INDEX IF NOT EXISTS idx_scripts_search_fts
    ON scripts USING gin (script_fts(display_name, name, description, tags, params));
