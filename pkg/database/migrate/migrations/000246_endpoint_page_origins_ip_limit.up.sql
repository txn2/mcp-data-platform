-- 000246: a page's frame, connect and form origins (#2554), and a per-client
-- address limit on every endpoint (#2556).
--
-- Each origin list adds to one directive of a page answer's content policy:
-- frame-src, connect-src beside 'self', and form-action beside 'self'. An
-- endpoint that lists none answers the policy it answered before.
ALTER TABLE script_endpoints ADD COLUMN IF NOT EXISTS page_frame_origins TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE script_endpoints ADD COLUMN IF NOT EXISTS page_connect_origins TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE script_endpoints ADD COLUMN IF NOT EXISTS page_form_origins TEXT[] NOT NULL DEFAULT '{}';

-- One client address's requests a minute on each replica; 0 is no limit,
-- which is what every endpoint bound before this has.
ALTER TABLE script_endpoints ADD COLUMN IF NOT EXISTS ip_rate_limit INTEGER NOT NULL DEFAULT 0
    CHECK (ip_rate_limit >= 0);

-- A request over that limit is a refusal of its own, counted beside the others.
ALTER TABLE script_endpoint_refusals DROP CONSTRAINT IF EXISTS script_endpoint_refusals_reason_check;
ALTER TABLE script_endpoint_refusals ADD CONSTRAINT script_endpoint_refusals_reason_check
    CHECK (reason IN ('bot_check', 'rate_limit', 'caller_rate_limit', 'ip_rate_limit', 'body_limit', 'credential', 'caller', 'origin'));
