-- 000113: script embeddings
--
-- Inline embedding columns on the scripts table backing semantic discovery for
-- managed scripts (#1370). Scripts were the only kind of knowledge the platform
-- holds with no consumer of the shared indexjobs framework, so a script was
-- found only when the words a caller typed matched the words its author wrote.
--
-- A script is a single embeddable unit, so its vector lives on the row itself,
-- mirroring prompts (000062) and memory_records (000054) rather than a
-- dedicated vector table. SourceID for the indexjobs scripts consumer is the
-- script id; each unit yields exactly one item.
--
-- What is embedded is the script's description card -- title, description,
-- parameter names, tags, and the one line stating whether anything will execute
-- it -- and never its Starlark. docs/scripts/security.md admits the contract to
-- anyone the scope rules admit and the source only to the owner and to
-- administrators; one vector per row cannot be split along that line, so a
-- vector built partly from source would let code a caller may not read decide
-- how their results rank.
--
--   1. embedding / embedding_model / embedding_text_hash: the vector plus the
--      provider-identity and content-hash breadcrumbs pkg/indexjobs needs to
--      dedup re-embeds by text hash and to detect model-swap gaps. Dim is not
--      stored: it is derivable from the stored vector's length.
--
--      embedding_text_hash is BYTEA, as every sibling embedding column is.
--      indexjobs.TextHash returns a raw SHA-256, and raw digest bytes are not a
--      UTF-8 string: declaring the column TEXT is what made the calls kind fail
--      on every write, forever, while reporting nothing (#1365, fixed by
--      000112).
--
--   2. hnsw ANN index on embedding: matches the cosine `<=>` operator the
--      ranked search uses (vector_cosine_ops). Requires pgvector >= 0.5.0,
--      which the platform's pgvector image ships.
--
-- The lexical arm needs nothing here: script_fts and idx_scripts_search_fts
-- already exist (000102, rebuilt self-contained by 000111).
--
-- pgvector is enabled by migration 000031; re-enable defensively so this
-- migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE scripts
    ADD COLUMN IF NOT EXISTS embedding           vector(768),
    ADD COLUMN IF NOT EXISTS embedding_model     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding_text_hash BYTEA;

CREATE INDEX IF NOT EXISTS idx_scripts_embedding_hnsw
    ON scripts USING hnsw (embedding vector_cosine_ops);
