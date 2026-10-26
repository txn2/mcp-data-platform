-- 000221 down: one deployment-wide bot check and the signed_in and public
-- modes again. The check named "default" becomes the section; an endpoint
-- that named another check is checked by it.

INSERT INTO platform_settings (section, value, updated_by)
SELECT 'bot_check',
       jsonb_build_object('provider', c.provider, 'site_key', c.site_key, 'secret_name', c.secret_name,
                          'allow_public_without_check',
                          EXISTS (SELECT 1 FROM script_endpoints WHERE auth_mode = 'public'),
                          'difficulty', c.difficulty, 'max_difficulty', c.max_difficulty,
                          'rate_threshold', c.rate_threshold, 'challenge_ttl_seconds', c.challenge_ttl_seconds),
       c.updated_by
  FROM bot_checks c
 WHERE c.name = 'default'
ON CONFLICT (section) DO NOTHING;

ALTER TABLE script_endpoints DROP CONSTRAINT IF EXISTS script_endpoints_bot_check_named;
ALTER TABLE script_endpoints DROP CONSTRAINT IF EXISTS script_endpoints_auth_mode_check;
ALTER TABLE script_endpoints ADD COLUMN IF NOT EXISTS open_reads BOOLEAN NOT NULL DEFAULT FALSE;

-- A bot_check endpoint that checked only some methods opened its reads.
UPDATE script_endpoints
   SET auth_mode = 'public',
       open_reads = cardinality(checked_methods) > 0 AND NOT ('GET' = ANY(checked_methods))
 WHERE auth_mode = 'bot_check';
UPDATE script_endpoints SET auth_mode = 'signed_in' WHERE auth_mode = 'authenticated';

DROP INDEX IF EXISTS idx_script_endpoints_bot_check;
ALTER TABLE script_endpoints
    DROP COLUMN IF EXISTS callers,
    DROP COLUMN IF EXISTS bot_check,
    DROP COLUMN IF EXISTS checked_methods;
ALTER TABLE script_endpoints
    ADD CONSTRAINT script_endpoints_auth_mode_check CHECK (auth_mode IN ('public', 'signed_in'));

DROP TABLE IF EXISTS bot_checks;

DELETE FROM script_endpoint_refusals WHERE reason = 'caller';
ALTER TABLE script_endpoint_refusals DROP CONSTRAINT IF EXISTS script_endpoint_refusals_reason_check;
ALTER TABLE script_endpoint_refusals ADD CONSTRAINT script_endpoint_refusals_reason_check
    CHECK (reason IN ('bot_check', 'rate_limit', 'caller_rate_limit', 'body_limit', 'credential', 'origin'));
