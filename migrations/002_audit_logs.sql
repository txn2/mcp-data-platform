-- Migration: 002_audit_logs
-- Creates tables for audit logging

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

-- Create initial partition for current month
CREATE TABLE IF NOT EXISTS audit_logs_default PARTITION OF audit_logs DEFAULT;

-- Indexes for common queries
CREATE INDEX idx_audit_logs_timestamp ON audit_logs(timestamp);
CREATE INDEX idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_tool_name ON audit_logs(tool_name);
CREATE INDEX idx_audit_logs_success ON audit_logs(success);
CREATE INDEX idx_audit_logs_created_date ON audit_logs(created_date);

-- Function to create monthly partitions
CREATE OR REPLACE FUNCTION create_audit_log_partition(partition_date DATE)
RETURNS void AS $$
DECLARE
    partition_name TEXT;
    start_date DATE;
    end_date DATE;
BEGIN
    partition_name := 'audit_logs_' || to_char(partition_date, 'YYYY_MM');
    start_date := date_trunc('month', partition_date);
    end_date := start_date + INTERVAL '1 month';

    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_logs FOR VALUES FROM (%L) TO (%L)',
        partition_name, start_date, end_date
    );
END;
$$ LANGUAGE plpgsql;

-- Create partitions for current and next month
SELECT create_audit_log_partition(CURRENT_DATE);
SELECT create_audit_log_partition(CURRENT_DATE + INTERVAL '1 month');
