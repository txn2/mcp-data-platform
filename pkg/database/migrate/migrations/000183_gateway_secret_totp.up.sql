-- 000183: a stored secret may hold an authenticator seed (#2065).
--
-- An automation that signs in to a vendor portal as a person needs the six-
-- or eight-digit code an authenticator app shows, which changes every period
-- and so cannot be stored. It is a function of the app's seed and the time
-- (RFC 6238), so a secret of kind totp holds the seed, encrypted in value
-- like any secret, and a caller writes {{totp:<name>}} where the code goes.
--
-- totp_algorithm, totp_digits and totp_period are the code's parameters,
-- read from the otpauth URI the seed was pasted as; a value secret leaves
-- them at their defaults. totp_last_period is the last period a code was
-- issued for, kept in the row so every replica sees it: a provider refuses a
-- code it already accepted in the same period, so a second fill waits for the
-- next period instead of sending the same code twice.
ALTER TABLE gateway_secrets
    ADD COLUMN IF NOT EXISTS kind             TEXT    NOT NULL DEFAULT 'value',
    ADD COLUMN IF NOT EXISTS totp_algorithm   TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS totp_digits      INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS totp_period      INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS totp_last_period BIGINT  NOT NULL DEFAULT 0;
