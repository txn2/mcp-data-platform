-- 000187: tool calls executed as MCP tasks (#2009).
--
-- A client that declares the MCP Tasks extension is answered with a task
-- handle when it calls an export tool, and the export runs apart from the
-- request on the replica that took the call. Each row is one such call: who
-- started it (only they and an administrator may follow it), where it stands,
-- and, once it ends, the tool's result or the JSON-RPC error it failed with,
-- which tasks/get hands back on any replica.
--
-- The replica executing a call stamps heartbeat_at every few seconds and reads
-- cancel_requested_at in the same statement, which is how a tasks/cancel taken
-- by another replica reaches it. A working row whose heartbeat stopped belongs
-- to a replica that is gone and is read as failed. A managed-script run is a
-- task too, and is not kept here: the run is its own record.
CREATE TABLE IF NOT EXISTS call_tasks (
    id                  TEXT        PRIMARY KEY,
    tool_name           TEXT        NOT NULL,
    user_id             TEXT        NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'working'
                                    CHECK (status IN ('working', 'completed', 'failed', 'cancelled')),
    status_message      TEXT        NOT NULL DEFAULT '',
    result              JSONB,
    error               JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    heartbeat_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    cancel_requested_at TIMESTAMPTZ,
    expires_at          TIMESTAMPTZ NOT NULL
);

-- The retention pass deletes the rows past their expiry.
CREATE INDEX IF NOT EXISTS idx_call_tasks_expires_at ON call_tasks(expires_at);
