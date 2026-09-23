-- Run grants on managed scripts and caller attributes on API keys (#1846).
--
-- api_keys.attributes are named values a key carries into every call as
-- claims. A script parameter bound to caller.<name> takes its value from
-- there, so a multi-tenant application's key names its tenant and no request
-- body can claim another.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS attributes JSONB NOT NULL DEFAULT '{}';

-- script_grants lets someone other than the owner run a script over HTTP and
-- read the runs they started and the contract, without being able to read the
-- source or change the script. The run still executes as script:<name> with
-- its author's roles: a grant decides who may ask for a run, never what the
-- run may reach. A principal is a persona name, a role, or an API key name.
CREATE TABLE IF NOT EXISTS script_grants (
    script_id      UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    principal_kind TEXT        NOT NULL CHECK (principal_kind IN ('persona', 'role', 'api_key')),
    principal      TEXT        NOT NULL CHECK (principal <> ''),
    granted_by     TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (script_id, principal_kind, principal)
);

-- A caller's catalog is "every script granted to anything I am", read by
-- principal rather than by script.
CREATE INDEX IF NOT EXISTS idx_script_grants_principal
    ON script_grants (principal_kind, principal);
