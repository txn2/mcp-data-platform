-- 000112: store the call record's embed-text digest as bytes.
--
-- 000107 declared call_records.embedding_text_hash as TEXT. Every sibling
-- embedding table declares the same column BYTEA (000054, 000062, 000063,
-- 000070, 000091, 000096), and for good reason: indexjobs.TextHash returns a
-- raw SHA-256, and raw digest bytes are not a UTF-8 string. PostgreSQL refuses
-- them outright -- a digest containing 0x00 can never be stored in a text
-- column, and one containing any other invalid sequence is rejected by the
-- encoding check.
--
-- So callindex's UpsertVectors failed on every write, with the offending byte
-- differing per row because each row's digest differs. The kind never indexed
-- a single record, and because a failed unit is re-queued, it failed forever.
--
-- No deployment can hold a digest that was written through the failing path,
-- so the conversion has nothing to preserve in practice. It is still written
-- to preserve the bytes rather than assume: convert_to recovers the exact
-- octets of any value that did land, and NULLIF maps the DEFAULT '' -- which
-- is what every existing row holds -- onto NULL, the same "no hash yet" the
-- sibling tables use.
ALTER TABLE call_records
    ALTER COLUMN embedding_text_hash DROP DEFAULT;

ALTER TABLE call_records
    ALTER COLUMN embedding_text_hash DROP NOT NULL;

ALTER TABLE call_records
    ALTER COLUMN embedding_text_hash TYPE BYTEA
    USING convert_to(NULLIF(embedding_text_hash, ''), 'UTF8');
