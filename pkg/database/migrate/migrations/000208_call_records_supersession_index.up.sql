-- #2231: the supersession check a call record's outcome is read from looks for
-- a later call in the same session on the same kind and connection. It was
-- answered by intersecting the session index with the (kind, connection)
-- index, a fifth of a millisecond per record, which every list and count
-- filtered by outcome paid once per record in the catalog.
CREATE INDEX IF NOT EXISTS idx_call_records_supersession
    ON call_records(session_id, kind, connection, created_at);
