-- Supporting indexes for the session read model (issue #1318). A session is
-- derived from the audit log, and the two tables below answer "what did this
-- session leave behind" once per listed session. audit_logs already has
-- idx_audit_logs_session_id (migration 000004); these are the two joins that
-- had no index of their own.

-- Assets a session saved. Partial on the live rows because the read model
-- never counts a deleted asset as a session's output.
CREATE INDEX IF NOT EXISTS idx_portal_assets_session_id
    ON portal_assets(session_id) WHERE deleted_at IS NULL;

-- Insights a session captured. Since migration 000031 an insight is a
-- knowledge-dimension memory record whose capturing session lives in its
-- metadata, so the index is on the expression the lookup uses.
CREATE INDEX IF NOT EXISTS idx_memory_records_session_id
    ON memory_records((metadata->>'session_id'));
