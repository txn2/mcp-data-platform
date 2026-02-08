-- Drop index
DROP INDEX IF EXISTS idx_audit_logs_session_id;

-- Drop new columns
ALTER TABLE audit_logs DROP COLUMN IF EXISTS source;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS authorized;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS content_blocks;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS enrichment_applied;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS transport;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS request_chars;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS session_id;

-- Restore redundant column
ALTER TABLE audit_logs ADD COLUMN response_token_estimate INTEGER DEFAULT 0;
