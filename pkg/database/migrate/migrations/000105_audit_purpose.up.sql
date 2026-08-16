-- The one sentence the caller gave for WHY a call was made (issue #1317).
-- Recorded in its own column rather than in parameters: it is not an argument
-- value, so the parameter redaction policy does not apply to it, and operators
-- filter and read it on its own.
--
-- Nullable with no default: a NULL here means the row predates the column, which
-- is a different fact from an empty purpose on a call the platform did not gate.
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS purpose TEXT;
