ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS enrichment_match_kind VARCHAR(20) DEFAULT '';
