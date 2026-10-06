-- 000178: drop what 000177 kept for replicas on the previous image (#2029).
--
-- 000177 moved script embeddings to script_embedding_chunks and the lexical
-- index to the seven-argument script_fts(..., source). For the length of a
-- rolling deployment it left in place what a replica on the previous image
-- reads and writes: the inline scripts.embedding, embedding_model and
-- embedding_text_hash columns (000113), their HNSW index, and the five- and
-- six-argument script_fts overloads (000102/000111, 000116). Nothing in an
-- image carrying 000177 reads or writes any of them.
--
-- This must ship in a release after the one carrying 000177. A rollout from an
-- image older than 000177 straight to this one would leave the old replicas
-- calling functions and writing columns that no longer exist until it finished.
--
-- No index depends on the overloads being dropped: 000177 rebuilt
-- idx_scripts_search_fts on the seven-argument call.

DROP INDEX IF EXISTS idx_scripts_embedding_hnsw;

ALTER TABLE scripts
    DROP COLUMN IF EXISTS embedding,
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS embedding_text_hash;

DROP FUNCTION IF EXISTS script_fts(text, text, text, text, text[], jsonb);

DROP FUNCTION IF EXISTS script_fts(text, text, text, text[], jsonb);
