-- Reverse of 000118. The approval and grant columns return empty: which
-- version was approved, by whom, and what it was granted were removed by the
-- up migration and cannot be reconstructed. Every script therefore comes back
-- with no approved version, which under the pre-1403 code means nothing
-- executes it until a version is approved again.
ALTER TABLE script_versions
    ADD COLUMN IF NOT EXISTS approved_by TEXT NOT NULL DEFAULT '';
ALTER TABLE script_versions
    ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ;
ALTER TABLE script_versions
    ADD COLUMN IF NOT EXISTS auto_approved BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE script_versions
    ADD COLUMN IF NOT EXISTS grants JSONB NOT NULL DEFAULT '{}';

ALTER TABLE scripts ADD COLUMN IF NOT EXISTS approved_version_id UUID;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'scripts_approved_version_fk'
    ) THEN
        ALTER TABLE scripts
            ADD CONSTRAINT scripts_approved_version_fk
            FOREIGN KEY (approved_version_id) REFERENCES script_versions(id) ON DELETE RESTRICT;
    END IF;
END
$$;

ALTER TABLE scripts ALTER COLUMN status SET DEFAULT 'draft';
ALTER TABLE scripts DROP CONSTRAINT IF EXISTS scripts_status_check;
ALTER TABLE scripts
    ADD CONSTRAINT scripts_status_check
    CHECK (status IN ('draft', 'active', 'deprecated', 'superseded'));
