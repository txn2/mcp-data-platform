-- 000207: requests an endpoint refused before its handler ran (#2208).
--
-- An answered request writes an audit row; one refused at the bot check, a
-- rate limit, the body limit, its credential or its origin wrote nothing, and
-- a flood refused at the bot check is what an administrator looks for on a
-- public endpoint. Each replica counts its refusals in memory and adds them
-- here every few seconds, one row per endpoint, hour and reason, so a flood
-- is a counter moving rather than a row per request. Kept 30 days.
CREATE TABLE IF NOT EXISTS script_endpoint_refusals (
    endpoint_id UUID        NOT NULL REFERENCES script_endpoints(id) ON DELETE CASCADE,
    hour        TIMESTAMPTZ NOT NULL,
    reason      TEXT        NOT NULL
                            CHECK (reason IN ('bot_check', 'rate_limit', 'caller_rate_limit', 'body_limit', 'credential', 'origin')),
    count       BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (endpoint_id, hour, reason)
);

CREATE INDEX IF NOT EXISTS idx_script_endpoint_refusals_hour ON script_endpoint_refusals(hour);
