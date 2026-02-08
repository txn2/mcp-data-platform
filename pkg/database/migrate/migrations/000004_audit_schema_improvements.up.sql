-- Drop redundant column (response_token_estimate = response_chars / 4)
ALTER TABLE audit_logs DROP COLUMN IF EXISTS response_token_estimate;

-- Add session analytics
ALTER TABLE audit_logs ADD COLUMN session_id TEXT DEFAULT '';

-- Add request sizing
ALTER TABLE audit_logs ADD COLUMN request_chars INTEGER DEFAULT 0;

-- Add transport metadata
ALTER TABLE audit_logs ADD COLUMN transport VARCHAR(30) DEFAULT 'stdio';

-- Add enrichment tracking
ALTER TABLE audit_logs ADD COLUMN enrichment_applied BOOLEAN DEFAULT FALSE;

-- Add content block count
ALTER TABLE audit_logs ADD COLUMN content_blocks INTEGER DEFAULT 0;

-- Add authorization visibility
ALTER TABLE audit_logs ADD COLUMN authorized BOOLEAN DEFAULT TRUE;

-- Add source identification
ALTER TABLE audit_logs ADD COLUMN source VARCHAR(30) DEFAULT 'mcp';

-- Index for session-based queries
CREATE INDEX idx_audit_logs_session_id ON audit_logs(session_id);
