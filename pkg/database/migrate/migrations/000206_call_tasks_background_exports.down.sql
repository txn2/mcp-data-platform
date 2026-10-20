DROP INDEX IF EXISTS idx_call_tasks_background_ended;
DROP INDEX IF EXISTS idx_call_tasks_user_key;
DROP INDEX IF EXISTS idx_call_tasks_working_key;
ALTER TABLE call_tasks
    DROP COLUMN IF EXISTS progress,
    DROP COLUMN IF EXISTS background,
    DROP COLUMN IF EXISTS idempotency_key;
