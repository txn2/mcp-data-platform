-- Audit Logs (partitioned by date for efficient retention management)
CREATE TABLE IF NOT EXISTS audit_logs (
    id              VARCHAR(32) NOT NULL,
    timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    duration_ms     INTEGER,
    request_id      VARCHAR(255),
    user_id         VARCHAR(255),
    user_email      VARCHAR(255),
    persona         VARCHAR(100),
    tool_name       VARCHAR(255) NOT NULL,
    toolkit_kind    VARCHAR(100),
    toolkit_name    VARCHAR(100),
    connection      VARCHAR(100),
    parameters      JSONB,
    success         BOOLEAN NOT NULL,
    error_message   TEXT,
    created_date    DATE NOT NULL DEFAULT CURRENT_DATE,
    PRIMARY KEY (id, created_date)
) PARTITION BY RANGE (created_date);

-- Default partition for any dates not covered by specific partitions
CREATE TABLE IF NOT EXISTS audit_logs_default PARTITION OF audit_logs DEFAULT;

-- Indexes for common queries
CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp ON audit_logs(timestamp);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tool_name ON audit_logs(tool_name);
CREATE INDEX IF NOT EXISTS idx_audit_logs_success ON audit_logs(success);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_date ON audit_logs(created_date);
