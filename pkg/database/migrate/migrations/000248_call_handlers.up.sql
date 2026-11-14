-- Tool-call handlers (#2436, #2634, #2635, #2437): a policy decides whether a
-- tool call runs, a transform changes what it returns, a watch runs a script
-- after it. The three share one table, matched by connection, operation and
-- caller, and the read-only handler view lists them beside the other kinds.

CREATE TABLE IF NOT EXISTS call_handlers (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    script_id   UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    kind        TEXT        NOT NULL CHECK (kind IN ('policy', 'transform', 'watch')),
    -- connection is a connection name, or a pattern matching names.
    connection  TEXT        NOT NULL CHECK (connection <> ''),
    -- operations are operation ids, tool names, or METHOD path patterns.
    operations  TEXT[]      NOT NULL DEFAULT '{}',
    -- personas and users are who the handler applies to; both empty is
    -- everyone.
    personas    TEXT[]      NOT NULL DEFAULT '{}',
    users       TEXT[]      NOT NULL DEFAULT '{}',
    -- conditions are equalities on an argument: [{in, name, equals}].
    conditions  JSONB       NOT NULL DEFAULT '[]',
    -- args are the script's typed parameters, bound when it runs.
    args        JSONB       NOT NULL DEFAULT '{}',
    timeout_ms  INTEGER     NOT NULL DEFAULT 2000 CHECK (timeout_ms > 0 AND timeout_ms <= 60000),
    on_failure  TEXT        NOT NULL DEFAULT '' CHECK (on_failure IN ('', 'hold', 'deny', 'fail', 'original')),
    items_path  TEXT        NOT NULL DEFAULT '',
    -- writes overrides which operations count as writes: {operation: bool}.
    writes      JSONB       NOT NULL DEFAULT '{}',
    -- protected are the columns (catalog.schema.table.column) or JSON paths a
    -- transform protects.
    protected   TEXT[]      NOT NULL DEFAULT '{}',
    ord         INTEGER     NOT NULL DEFAULT 0,
    enabled     BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by  TEXT        NOT NULL DEFAULT '',
    updated_by  TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT call_handlers_original_unprotected CHECK (on_failure <> 'original' OR cardinality(protected) = 0)
);

CREATE INDEX IF NOT EXISTS idx_call_handlers_script ON call_handlers(script_id);
CREATE INDEX IF NOT EXISTS idx_call_handlers_connection ON call_handlers(connection) WHERE enabled;

-- Every replica keeps the enabled handlers in memory and reads them again
-- when this channel says they changed.
CREATE OR REPLACE FUNCTION call_handlers_changed() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('call_handlers', '');
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_call_handlers_changed ON call_handlers;
CREATE TRIGGER trg_call_handlers_changed
    AFTER INSERT OR UPDATE OR DELETE ON call_handlers
    FOR EACH STATEMENT
    EXECUTE FUNCTION call_handlers_changed();

-- A policy's rolling-window counts: one row per counted call under the key
-- the script named, read and written in one statement under a lock on the
-- key, so replicas agree on the count.
CREATE TABLE IF NOT EXISTS call_handler_counts (
    handler_id  UUID        NOT NULL REFERENCES call_handlers(id) ON DELETE CASCADE,
    key         TEXT        NOT NULL,
    call_id     TEXT        NOT NULL,
    amount      INTEGER     NOT NULL DEFAULT 1 CHECK (amount > 0),
    at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_call_handler_counts_key ON call_handler_counts(handler_id, key, at);
CREATE INDEX IF NOT EXISTS idx_call_handler_counts_call ON call_handler_counts(call_id);

-- What a policy or transform was given and printed when it failed, kept for
-- administrators for seven days. A handler that succeeds is recorded on the
-- call's audit row only.
CREATE TABLE IF NOT EXISTS call_handler_failures (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    handler_id  UUID        NOT NULL REFERENCES call_handlers(id) ON DELETE CASCADE,
    version     INTEGER     NOT NULL DEFAULT 0,
    user_email  TEXT        NOT NULL DEFAULT '',
    call        JSONB       NOT NULL DEFAULT '{}',
    prints      TEXT        NOT NULL DEFAULT '',
    error       TEXT        NOT NULL DEFAULT '',
    outcome     TEXT        NOT NULL DEFAULT '',
    duration_ms INTEGER     NOT NULL DEFAULT 0,
    at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_call_handler_failures_handler ON call_handler_failures(handler_id, at DESC);
CREATE INDEX IF NOT EXISTS idx_call_handler_failures_at ON call_handler_failures(at);

-- Each call's audit row records the handlers that matched it: their id,
-- kind, what each did and how long it took.
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS handlers JSONB;

-- A watch's run is a queued run carrying the call it watched, and runs with
-- the roles of the administrator who approved the version.
ALTER TABLE script_runs ADD COLUMN IF NOT EXISTS call JSONB;
ALTER TABLE script_runs ADD COLUMN IF NOT EXISTS run_as JSONB;
ALTER TABLE script_runs ADD COLUMN IF NOT EXISTS call_handler_id UUID REFERENCES call_handlers(id) ON DELETE SET NULL;
ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool', 'schedule', 'portal', 'event', 'watch'));
CREATE INDEX IF NOT EXISTS idx_script_runs_watch_backlog
    ON script_runs(call_handler_id) WHERE trigger_kind = 'watch' AND status = 'pending';

-- Approvals: a held call is a request of subject call, approved as an
-- allowance (a number of calls of one operation, for one person, on one
-- connection, until a deadline); a tool-call handler's version is approved
-- for the purpose call_handler, and the approver's roles are what it runs
-- with.
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS decided_roles TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS call_tool TEXT NOT NULL DEFAULT '';
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS call_connection TEXT NOT NULL DEFAULT '';
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS call_operation TEXT NOT NULL DEFAULT '';
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS call_arguments JSONB;
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS call_rendering TEXT NOT NULL DEFAULT '';
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS call_handlers JSONB NOT NULL DEFAULT '[]';
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS approvers TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS calls_asked INTEGER NOT NULL DEFAULT 0;
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS calls_approved INTEGER NOT NULL DEFAULT 0;
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS calls_used INTEGER NOT NULL DEFAULT 0;
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS allowance_until TIMESTAMPTZ;
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_subject_kind_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_kind_check
    CHECK (subject_kind IN ('script_version', 'site_release', 'site_environment', 'call'));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_purpose_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_purpose_check
    CHECK (purpose IN ('form_public', 'site_promote', 'site_public', 'call_handler', 'call_allowance'));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_subject_present;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_present
    CHECK ((subject_kind = 'script_version' AND script_id IS NOT NULL)
        OR (subject_kind = 'site_release' AND site_release_id IS NOT NULL)
        OR (subject_kind = 'site_environment' AND site_id IS NOT NULL AND target <> '')
        OR (subject_kind = 'call' AND call_tool <> '' AND call_connection <> '' AND calls_asked > 0));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_version_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_version_check
    CHECK (version > 0 OR subject_kind IN ('site_environment', 'call'));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_calls_used_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_calls_used_check
    CHECK (calls_used >= 0 AND calls_used <= calls_approved);
-- An approved allowance is found by who may spend it.
CREATE INDEX IF NOT EXISTS idx_approval_requests_allowance
    ON approval_requests (requester_email, call_connection, call_operation)
    WHERE subject_kind = 'call' AND status = 'approved';

CREATE OR REPLACE VIEW script_handlers AS
SELECT e.id, 'endpoint'::text AS kind,
       CASE WHEN s.is_form THEN 'form' ELSE 'endpoint' END AS type,
       e.script_id, e.name AS target, e.enabled, e.created_by, e.created_at
  FROM script_endpoints e JOIN scripts s ON s.id = e.script_id
UNION ALL
SELECT c.id, 'schedule'::text, 'schedule'::text,
       c.script_id, c.cron_spec || ' ' || c.timezone, c.enabled, c.created_by, c.created_at
  FROM script_schedules c
UNION ALL
SELECT w.id, 'webhook'::text, 'webhook'::text,
       w.script_id, w.source, w.enabled, w.created_by, w.created_at
  FROM script_webhook_handlers w
UNION ALL
SELECT h.id, 'policy'::text, 'policy'::text,
       h.script_id, h.connection || ' ' || array_to_string(h.operations, ', '), h.enabled, h.created_by, h.created_at
  FROM call_handlers h WHERE h.kind = 'policy'
UNION ALL
SELECT h.id, 'transform'::text, 'transform'::text,
       h.script_id, h.connection || ' ' || array_to_string(h.operations, ', '), h.enabled, h.created_by, h.created_at
  FROM call_handlers h WHERE h.kind = 'transform'
UNION ALL
SELECT h.id, 'watch'::text, 'watch'::text,
       h.script_id, h.connection || ' ' || array_to_string(h.operations, ', '), h.enabled, h.created_by, h.created_at
  FROM call_handlers h WHERE h.kind = 'watch';
