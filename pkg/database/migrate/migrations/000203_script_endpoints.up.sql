-- 000203: endpoints, scripts bound to HTTP routes (#2171, #2172, #2173).
--
-- A script has AT MOST ONE binding (script_id is UNIQUE), as it has at most
-- one schedule, and an endpoint's name is unique across the deployment: it is
-- the first segment of /endpoints/{name}/..., so two bindings cannot share it.
--
-- A binding is enabled and disabled on its own, without touching the script.
-- Its limits are per endpoint: timeout_seconds, max_memory_bytes and
-- max_body_bytes bound one request, rate_limit is the endpoint's requests per
-- minute and caller_rate_limit a signed-in caller's.
CREATE TABLE IF NOT EXISTS script_endpoints (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    script_id         UUID        NOT NULL UNIQUE REFERENCES scripts(id) ON DELETE CASCADE,
    name              TEXT        NOT NULL UNIQUE,
    methods           TEXT[]      NOT NULL,
    -- path is the template under /endpoints/{name}, "" for the name alone
    -- and "/{id}" for one path parameter.
    path              TEXT        NOT NULL DEFAULT '',
    auth_mode         TEXT        NOT NULL CHECK (auth_mode IN ('public', 'signed_in')),
    -- open_reads lets GET and HEAD on a public endpoint through without a bot
    -- check: a read-only lookup a page loads (#2174).
    open_reads        BOOLEAN     NOT NULL DEFAULT FALSE,
    timeout_seconds   INTEGER     NOT NULL,
    max_memory_bytes  BIGINT      NOT NULL,
    max_body_bytes    INTEGER     NOT NULL,
    allowed_origins   TEXT[]      NOT NULL DEFAULT '{}',
    rate_limit        INTEGER     NOT NULL,
    caller_rate_limit INTEGER     NOT NULL,
    enabled           BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by        TEXT        NOT NULL DEFAULT '',
    updated_by        TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A request writes no script_runs row. One that failed is kept here, with its
-- error, for the script's owner to read, and swept after a few days by the
-- retention loop.
CREATE TABLE IF NOT EXISTS script_endpoint_failures (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id UUID        NOT NULL REFERENCES script_endpoints(id) ON DELETE CASCADE,
    version     INTEGER     NOT NULL,
    method      TEXT        NOT NULL,
    status      INTEGER     NOT NULL,
    error       TEXT        NOT NULL,
    duration_ms INTEGER     NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_script_endpoint_failures_endpoint
    ON script_endpoint_failures(endpoint_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_script_endpoint_failures_created
    ON script_endpoint_failures(created_at);

-- Each answered bot check challenge, recorded as used the moment it is
-- accepted (#2173). The primary key is what refuses a replay on any replica.
-- The rows of the last minute are also the endpoint's recent request rate the
-- next challenge's difficulty is set from. A row outlives its challenge only
-- until the retention loop sweeps it.
CREATE TABLE IF NOT EXISTS bot_check_answers (
    challenge_id TEXT        PRIMARY KEY,
    endpoint     TEXT        NOT NULL,
    used_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_bot_check_answers_recent
    ON bot_check_answers(endpoint, used_at);
CREATE INDEX IF NOT EXISTS idx_bot_check_answers_expires
    ON bot_check_answers(expires_at);
