-- 000177: a script is indexed on its source as well as its card (#2027).
--
-- A script's definition is readable by everyone signed in (#1866), and its
-- source is where the reasoning behind its rules lives: the comments say why,
-- the SQL names the tables it reads and writes. A question like "which script
-- writes sales.orders" is answered from the source, so search reads it.
--
-- Semantic arm. A source runs up to 256 KiB and the embedder takes a few KB per
-- call, so a script is embedded as a SET of chunks (the card, then the source in
-- pieces), the way knowledge pages are (000097): one row per chunk. Search
-- scores every script in service by its best chunk, exactly; there is no ANN
-- index, because a pool of nearest chunks can be filled by one long script and
-- a script corpus is small enough to scan. The set-level state lives on the
-- script row:
--
--   index_text_hash      the hash of what the script is indexed on now, written
--                        by every save. A save that changes it leaves the
--                        script's chunks in place for the worker to reuse
--                        (it re-embeds only the chunks whose text moved), so a
--                        script ranks on its previous chunks until the rebuild
--                        lands.
--   index_embedded_hash  the hash of what the current chunks were built from,
--                        written by the index worker when it finishes.
--   index_model          the provider model that built them.
--
-- A script is owed an embedding while index_embedded_hash differs from
-- index_text_hash or index_model differs from the provider's. NULL is "never
-- built", so every existing script is owed one after this migration and the
-- reconciler's next sweep builds it.
--
-- Lexical arm. script_fts gains the source, weighted below the card (A for the
-- card, D for the source), so a script whose description matches outranks one
-- that only mentions the words in its code.
--
-- Rolling deployment, as 000116 records: replicas on the previous image keep
-- calling the six-argument script_fts and keep reading and writing the inline
-- scripts.embedding columns, so neither the old overloads nor those columns are
-- dropped here. Only the FTS index moves to the seven-argument call; an old
-- replica's lexical query still answers, on a sequential scan, for the length
-- of a rollout. The function calls only built-ins (000102, 000111).

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS script_embedding_chunks (
    script_id   UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    chunk_index INTEGER     NOT NULL,
    text_hash   BYTEA       NOT NULL,
    embedding   vector(768) NOT NULL,
    model       TEXT        NOT NULL DEFAULT '',
    dim         INTEGER     NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (script_id, chunk_index)
);

ALTER TABLE scripts
    ADD COLUMN IF NOT EXISTS index_text_hash     BYTEA,
    ADD COLUMN IF NOT EXISTS index_embedded_hash BYTEA,
    ADD COLUMN IF NOT EXISTS index_model         TEXT;

DROP INDEX IF EXISTS idx_scripts_search_fts;

CREATE OR REPLACE FUNCTION script_fts(
    display_name text, name text, description text, category text, tags text[], params jsonb, source text
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT setweight(to_tsvector('english',
        coalesce(nullif(display_name, ''), name) || ' ' ||
        coalesce(description, '')                || ' ' ||
        coalesce(category, '')                   || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        CASE WHEN jsonb_typeof(params) = 'array' THEN
            coalesce((
                SELECT string_agg(
                    coalesce(p->>'name', '') || ' ' || coalesce(p->>'description', ''), ' ')
                FROM jsonb_array_elements(params) AS p), '')
        ELSE '' END), 'A')
    || setweight(to_tsvector('english', coalesce(source, '')), 'D');
$$;

CREATE INDEX IF NOT EXISTS idx_scripts_search_fts
    ON scripts USING gin (script_fts(display_name, name, description, category, tags, params, source_code));
