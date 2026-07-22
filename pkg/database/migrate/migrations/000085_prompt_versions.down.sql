DROP INDEX IF EXISTS idx_audit_logs_prompt_serve;
ALTER TABLE prompts DROP COLUMN IF EXISTS version;
DROP INDEX IF EXISTS idx_prompt_versions_prompt;
DROP TABLE IF EXISTS prompt_versions;
