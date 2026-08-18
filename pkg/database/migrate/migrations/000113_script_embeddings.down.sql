-- Reverse 000113. Drop the script embedding index and columns.
--
-- The vectors are discarded, which costs nothing: a missing embedding is the
-- "not indexed yet" state the reconciler converges on its own, and ranking
-- degrades to the lexical arm that has always been there.

DROP INDEX IF EXISTS idx_scripts_embedding_hnsw;

ALTER TABLE scripts
    DROP COLUMN IF EXISTS embedding_text_hash,
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS embedding;
