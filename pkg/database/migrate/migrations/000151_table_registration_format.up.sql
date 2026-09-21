-- A registration reads its file through the reader its format names (#1820).
-- Until now every registration was over a CSV, read by Trino's line-based CSV
-- reader, which cannot carry a line break inside a value and reads a null back
-- as an empty string. A JSON-lines file is read by the JSON reader instead,
-- which carries every string exactly, and the CREATE TABLE a registration runs
-- -- at registration, at every follow, and when a failed follow puts a table
-- back -- has to name the reader its file needs.
--
-- format is that reader: 'csv' or 'jsonl'. Every existing row is a CSV
-- registration, which is what the default records.
ALTER TABLE table_registrations
    ADD COLUMN IF NOT EXISTS format TEXT NOT NULL DEFAULT 'csv';
