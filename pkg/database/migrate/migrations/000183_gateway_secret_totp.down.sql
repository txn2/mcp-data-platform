-- 000183 added authenticator seeds. A totp secret's value is a seed, which
-- the binary rolled back to would send as a value, so those rows go with the
-- columns.
DELETE FROM gateway_secrets WHERE kind = 'totp';
ALTER TABLE gateway_secrets
    DROP COLUMN IF EXISTS kind,
    DROP COLUMN IF EXISTS totp_algorithm,
    DROP COLUMN IF EXISTS totp_digits,
    DROP COLUMN IF EXISTS totp_period,
    DROP COLUMN IF EXISTS totp_last_period;
