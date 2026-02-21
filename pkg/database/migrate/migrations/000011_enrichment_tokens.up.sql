ALTER TABLE audit_logs ADD COLUMN enrichment_tokens_full INTEGER DEFAULT 0;
ALTER TABLE audit_logs ADD COLUMN enrichment_tokens_dedup INTEGER DEFAULT 0;
