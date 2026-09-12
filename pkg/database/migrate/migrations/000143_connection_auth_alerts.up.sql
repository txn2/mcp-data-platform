-- The connections whose stored credential was discarded and nobody has come
-- back to (#1694).
--
-- A revoked connection was already observable: an auth-event row records it and
-- the OAuth status card reports it. Both are pull. This table is what makes the
-- revocation push — it holds the incident between the moment the credential is
-- discarded and the moment somebody reauthorizes, which is the only window in
-- which there is anything to say.
--
-- One row per connection, inserted when the credential is discarded and deleted
-- when the connection is authorized again. The insert is conditional on the row
-- being absent, so a connection that fails repeatedly is announced once rather
-- than once per rejected call; deleting on reauthorization is what makes a
-- later revocation news again.
--
-- escalated_at is stamped by the sweep that tells the operator's chosen
-- recipients when the person who authorized the connection has not acted. It is
-- claimed with one conditional UPDATE, which is both the "already sent" check
-- and the single-winner election among replicas.
CREATE TABLE IF NOT EXISTS connection_auth_alerts (
    connection_kind TEXT        NOT NULL,              -- mcp, api, graphql
    connection_name TEXT        NOT NULL,              -- the connection within the kind
    authorized_by   TEXT        NOT NULL DEFAULT '',   -- whose authorization lapsed
    idp_host        TEXT        NOT NULL DEFAULT '',   -- the upstream that rejected it
    reason          TEXT        NOT NULL DEFAULT '',   -- RFC 6749 code, or why it was never called
    revoked_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    escalated_at    TIMESTAMPTZ,                       -- NULL until the operator's recipients are told
    PRIMARY KEY (connection_kind, connection_name)
);

-- The sweep reads exactly this: the rows old enough to escalate that have not
-- been escalated yet. A partial index keeps it off the rows already handled,
-- which on a healthy deployment is all of them.
CREATE INDEX IF NOT EXISTS idx_connection_auth_alerts_pending
    ON connection_auth_alerts (revoked_at)
    WHERE escalated_at IS NULL;
