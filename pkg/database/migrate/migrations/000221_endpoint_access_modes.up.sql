-- 000221: endpoint access modes, callers and named bot checks (#2259).
--
-- An endpoint has one access mode: authenticated (a platform API key or a
-- browser session, limited to the binding's callers), public (anyone, no
-- check) or bot_check (anyone who passes the named check). The one
-- deployment-wide bot check, platform_settings section "bot_check", becomes
-- the list of named checks below, and what it held becomes the check named
-- "default".

-- A named bot check an endpoint picks. A check an endpoint names cannot be
-- deleted: script_endpoints.bot_check references it ON DELETE RESTRICT.
CREATE TABLE IF NOT EXISTS bot_checks (
    name                  TEXT        PRIMARY KEY,
    provider              TEXT        NOT NULL CHECK (provider IN ('pow', 'turnstile', 'hcaptcha')),
    -- site_key and secret_name are Turnstile's and hCaptcha's: the widget's
    -- public key and the stored secret holding the provider's secret key.
    site_key              TEXT        NOT NULL DEFAULT '',
    secret_name           TEXT        NOT NULL DEFAULT '',
    -- The proof of work's leading zero bits while an endpoint is quiet, the
    -- most a flood raises them to, and the answers a minute past which they
    -- rise.
    difficulty            INTEGER     NOT NULL,
    max_difficulty        INTEGER     NOT NULL,
    rate_threshold        INTEGER     NOT NULL,
    challenge_ttl_seconds INTEGER     NOT NULL,
    created_by            TEXT        NOT NULL DEFAULT '',
    updated_by            TEXT        NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The deployment's section becomes "default". A section never written was
-- the proof of work at its defaults, and so was one set to none, which no
-- longer is a provider.
INSERT INTO bot_checks (name, provider, site_key, secret_name, difficulty, max_difficulty, rate_threshold,
                        challenge_ttl_seconds, created_by, updated_by)
SELECT 'default',
       CASE WHEN s.value->>'provider' IN ('turnstile', 'hcaptcha') THEN s.value->>'provider' ELSE 'pow' END,
       COALESCE(s.value->>'site_key', ''),
       COALESCE(s.value->>'secret_name', ''),
       COALESCE(NULLIF(s.value->>'difficulty', '')::int, 16),
       COALESCE(NULLIF(s.value->>'max_difficulty', '')::int, 22),
       COALESCE(NULLIF(s.value->>'rate_threshold', '')::int, 30),
       COALESCE(NULLIF(s.value->>'challenge_ttl_seconds', '')::int, 300),
       COALESCE(s.updated_by, ''), COALESCE(s.updated_by, '')
  FROM (SELECT 1) AS one
  LEFT JOIN platform_settings s ON s.section = 'bot_check'
ON CONFLICT (name) DO NOTHING;

ALTER TABLE script_endpoints DROP CONSTRAINT IF EXISTS script_endpoints_auth_mode_check;

-- callers is who an authenticated endpoint admits: '*', 'persona:<name>' or
-- 'api_key:<name>'; empty admits no one. bot_check is the check a bot_check
-- endpoint names, and checked_methods the methods it checks, empty for every
-- method it answers.
ALTER TABLE script_endpoints
    ADD COLUMN IF NOT EXISTS callers         TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS bot_check       TEXT   REFERENCES bot_checks(name) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS checked_methods TEXT[] NOT NULL DEFAULT '{}';

-- A signed_in endpoint admitted every authenticated caller, and still does.
UPDATE script_endpoints SET auth_mode = 'authenticated', callers = '{*}' WHERE auth_mode = 'signed_in';

-- A public endpoint answered with no check only where the section was none
-- and an administrator allowed it, or where it answered only reads and opened
-- them: it is public now.
UPDATE script_endpoints e
   SET auth_mode = 'public'
 WHERE e.auth_mode = 'public'
   AND (EXISTS (SELECT 1 FROM platform_settings s
                 WHERE s.section = 'bot_check' AND s.value->>'provider' = 'none'
                   AND COALESCE((s.value->>'allow_public_without_check')::boolean, FALSE))
        OR (e.open_reads AND e.methods <@ ARRAY['GET', 'HEAD']::text[]));

-- Every other public endpoint is checked by "default". One whose reads were
-- open checks the rest of its methods; one the section refused (none, not
-- allowed) answered nothing and is turned off until an administrator turns
-- it on.
UPDATE script_endpoints e
   SET auth_mode = 'bot_check',
       bot_check = 'default',
       checked_methods = CASE WHEN e.open_reads
                              THEN ARRAY(SELECT m FROM unnest(e.methods) AS m WHERE m NOT IN ('GET', 'HEAD'))
                              ELSE '{}' END,
       enabled = e.enabled AND NOT EXISTS (
           SELECT 1 FROM platform_settings s
            WHERE s.section = 'bot_check' AND s.value->>'provider' = 'none')
 WHERE e.auth_mode = 'public'
   AND NOT (EXISTS (SELECT 1 FROM platform_settings s
                     WHERE s.section = 'bot_check' AND s.value->>'provider' = 'none'
                       AND COALESCE((s.value->>'allow_public_without_check')::boolean, FALSE))
            OR (e.open_reads AND e.methods <@ ARRAY['GET', 'HEAD']::text[]));

ALTER TABLE script_endpoints DROP COLUMN IF EXISTS open_reads;

ALTER TABLE script_endpoints
    ADD CONSTRAINT script_endpoints_auth_mode_check CHECK (auth_mode IN ('authenticated', 'public', 'bot_check')),
    ADD CONSTRAINT script_endpoints_bot_check_named CHECK ((auth_mode = 'bot_check') = (bot_check IS NOT NULL));

CREATE INDEX IF NOT EXISTS idx_script_endpoints_bot_check ON script_endpoints(bot_check) WHERE bot_check IS NOT NULL;

DELETE FROM platform_settings WHERE section = 'bot_check';

-- An authenticated caller the binding's callers do not name is a refusal of
-- its own, counted beside the others (000207).
ALTER TABLE script_endpoint_refusals DROP CONSTRAINT IF EXISTS script_endpoint_refusals_reason_check;
ALTER TABLE script_endpoint_refusals ADD CONSTRAINT script_endpoint_refusals_reason_check
    CHECK (reason IN ('bot_check', 'rate_limit', 'caller_rate_limit', 'body_limit', 'credential', 'caller', 'origin'));
