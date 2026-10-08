-- 000180: stored secrets a request references by placeholder (#2051).
--
-- A managed script sometimes has to put a credential into the request it
-- sends rather than into its connection's authentication: a password typed
-- into a vendor's login form through a WebDriver connection, a key an API
-- wants in a JSON body field. The caller writes {{secret:<name>}} and the
-- api gateway fills in the value as it sends the request, so the script's
-- arguments, its recording, the audit row and the call record hold only the
-- placeholder.
--
-- value is encrypted by the platform's field encryptor before it is written,
-- and no read path returns it except the gateway's at send time.
-- allow_connections names the connections a secret may be sent through, and
-- is required: a secret not scoped to a connection could be sent anywhere.
-- allow_personas narrows who may reference it; empty means any persona that
-- can reach an allowed connection.
CREATE TABLE IF NOT EXISTS gateway_secrets (
    name              TEXT        PRIMARY KEY,
    description       TEXT        NOT NULL DEFAULT '',
    value             TEXT        NOT NULL,
    allow_connections TEXT[]      NOT NULL DEFAULT '{}',
    allow_personas    TEXT[]      NOT NULL DEFAULT '{}',
    created_by        TEXT        NOT NULL DEFAULT '',
    updated_by        TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
