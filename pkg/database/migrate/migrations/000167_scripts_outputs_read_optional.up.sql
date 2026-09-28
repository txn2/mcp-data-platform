-- 000167: a script's tests read every output they produce (#1952).
--
-- A script created from now on is saved only while every output its tests'
-- executions produce -- each export's columns, the staged state, each
-- notification, each published region, platform.result -- is read by one of
-- its tests. Every script that exists now was saved before that was asked of
-- it, and keeps saving as it does. The column is added TRUE and its default
-- then becomes FALSE, as 000165 and 000166 do.
ALTER TABLE scripts ADD COLUMN IF NOT EXISTS outputs_read_optional BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE scripts ALTER COLUMN outputs_read_optional SET DEFAULT FALSE;
