-- 000232: the Approvals queue's version subject and form previews (#2494,
-- part of #2437).
--
-- approval_requests is one request for a decision. This release has one
-- subject kind, a script version (subject_kind 'script_version'), and one purpose,
-- serving a form to callers with no session (purpose 'form_public'). The
-- version is named by number and fixed by the sha256 of its source, so what
-- is approved is exactly the source the approver was shown. A request is
-- decided once; status moves from pending to approved, denied or withdrawn,
-- never back. self_approved marks an administrator deciding their own.
CREATE TABLE IF NOT EXISTS approval_requests (
    id              TEXT        NOT NULL PRIMARY KEY,
    subject_kind    TEXT        NOT NULL CHECK (subject_kind IN ('script_version')),
    purpose         TEXT        NOT NULL CHECK (purpose IN ('form_public')),
    script_id       UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    version         INTEGER     NOT NULL CHECK (version > 0),
    source_sha256   TEXT        NOT NULL,
    requester_email TEXT        NOT NULL,
    requester_persona TEXT      NOT NULL DEFAULT '',
    reason          TEXT        NOT NULL DEFAULT '',
    -- changes is what changed since the version last approved for the same
    -- purpose: that version's number and a unified diff of the source.
    changes         JSONB       NOT NULL DEFAULT '{}',
    status          TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'denied', 'withdrawn')),
    decided_by      TEXT        NOT NULL DEFAULT '',
    decided_at      TIMESTAMPTZ,
    decision_comment TEXT       NOT NULL DEFAULT '',
    self_approved   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_approval_requests_status
    ON approval_requests (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_approval_requests_subject
    ON approval_requests (script_id, purpose, status, version DESC);
-- One request waits per script version and purpose.
CREATE UNIQUE INDEX IF NOT EXISTS idx_approval_requests_one_pending
    ON approval_requests (script_id, purpose, version) WHERE status = 'pending';

-- A form already served publicly keeps being served: its current version is
-- recorded as approved, decided by the upgrade. A form is a script defining
-- form_spec and form_submit; the platform reads that from the source, and
-- this reads it the same way lexically.
INSERT INTO approval_requests (id, subject_kind, purpose, script_id, version, source_sha256,
    requester_email, status, decided_by, decided_at, decision_comment)
SELECT 'apr_' || replace(gen_random_uuid()::text, '-', ''), 'script_version', 'form_public', s.id, v.version,
       encode(sha256(convert_to(v.source_code, 'UTF8')), 'hex'),
       s.owner_email, 'approved', 'upgrade', NOW(),
       'served publicly before approvals were required'
FROM script_endpoints e
JOIN scripts s ON s.id = e.script_id
JOIN script_versions v ON v.script_id = s.id AND v.version = COALESCE(s.released_version, s.version)
WHERE e.auth_mode IN ('public', 'bot_check')
  AND v.source_code ~ '(^|\n)def form_spec\('
  AND v.source_code ~ '(^|\n)def form_submit\(';

-- form_previews is one preview of a form a person opened (#2494): the saved
-- version it renders, or the unsaved source it was opened with. Only the
-- person who opened it can render it, and its form_spec and form_submit run
-- as a draft under the identity they opened it with (user_id, roles,
-- auth_type), captured as a version captures its author's. It expires within
-- the hour; the create that follows removes the expired ones.
CREATE TABLE IF NOT EXISTS form_previews (
    id          TEXT        NOT NULL PRIMARY KEY,
    script_id   UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    version     INTEGER     NOT NULL DEFAULT 0,
    source      TEXT        NOT NULL DEFAULT '',
    created_by  TEXT        NOT NULL,
    user_id     TEXT        NOT NULL,
    roles       JSONB       NOT NULL DEFAULT '[]',
    auth_type   TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_form_previews_expires ON form_previews (expires_at);
