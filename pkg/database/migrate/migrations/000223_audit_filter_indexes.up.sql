-- Indexes the audit events view's filter dropdowns read through (#2272). Each
-- dropdown lists one column's distinct values; with an index on the column the
-- store walks it one probe per value (a loose index scan) instead of reading
-- every row of audit_logs, which on tens of millions of rows is seconds per
-- dropdown on every page load. user_id and tool_name are indexed already
-- (000002).
CREATE INDEX IF NOT EXISTS idx_audit_logs_toolkit_kind ON audit_logs(toolkit_kind);
CREATE INDEX IF NOT EXISTS idx_audit_logs_source ON audit_logs(source);
CREATE INDEX IF NOT EXISTS idx_audit_logs_event_kind ON audit_logs(event_kind);
