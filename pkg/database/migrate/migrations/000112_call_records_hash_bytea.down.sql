-- Reverse 000112: return embedding_text_hash to the TEXT shape 000107 created.
--
-- The reverse discards the digests, and must. A digest written while the column
-- was BYTEA is raw bytes; encode()-ing it into text would hand 000107's shape a
-- value it never held, and convert_from() on those same bytes is the failure
-- the forward direction exists to end. Discarding costs nothing: an empty hash
-- means "no hash yet", so the worker re-embeds the record on its next pass.
ALTER TABLE call_records
    ALTER COLUMN embedding_text_hash TYPE TEXT USING '';

ALTER TABLE call_records
    ALTER COLUMN embedding_text_hash SET DEFAULT '';

ALTER TABLE call_records
    ALTER COLUMN embedding_text_hash SET NOT NULL;
