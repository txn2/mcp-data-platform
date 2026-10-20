-- 000206: an export run in the background for a client that does not declare
-- the Tasks extension (#2212).
--
-- graphql_export and api_export walked every page inside one tool call, and a
-- walk longer than the model client's own tool-call timeout failed on the
-- client's side with no asset and no handle to reconnect to. Such an export
-- now runs as a call task too, answered at once with its id and an
-- idempotency key, and read again by calling the export with the key.
--
-- background marks those rows, which platform_info reports to their caller
-- when they end. progress is how far the walk has come, written with the
-- heartbeat. The index holds one working run per caller, tool and key: a
-- retry with the key while the walk runs is answered with that run rather
-- than starting a second walk against the upstream.
ALTER TABLE call_tasks
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT,
    ADD COLUMN IF NOT EXISTS background      BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS progress        JSONB;

CREATE UNIQUE INDEX IF NOT EXISTS idx_call_tasks_working_key
    ON call_tasks(user_id, tool_name, idempotency_key)
    WHERE status = 'working' AND idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_call_tasks_user_key
    ON call_tasks(user_id, tool_name, idempotency_key, created_at DESC)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_call_tasks_background_ended
    ON call_tasks(user_id, updated_at)
    WHERE background AND status <> 'working';
