-- Sessions are searched by what their calls said they were for (issue #1322).
--
-- A session is derived from the audit log rather than stored, so there is no
-- session row to index: the words a session is found by live in the purpose of
-- each of its calls (migration 000105). Without this index, matching them means
-- recomputing to_tsvector over every audit row on every search.
--
-- The index is partial on the rows that carry a purpose. A NULL purpose is a row
-- written before the column existed and an empty one is a call the platform does
-- not gate; neither can ever match, and the search states both predicates so the
-- planner can use this index.
CREATE INDEX IF NOT EXISTS idx_audit_logs_purpose_fts
    ON audit_logs USING gin (to_tsvector('english', purpose))
    WHERE purpose IS NOT NULL AND purpose <> '';
