-- Hashing is one-way: the plaintext token and code values cannot be
-- restored, so rolling back to a plaintext-lookup binary requires
-- invalidating all outstanding credentials. This is user-visible: every
-- client's next refresh grant fails with invalid_grant, forcing a full
-- interactive re-authorization (browser sign-in), not a silent refresh.
DELETE FROM oauth_refresh_tokens;
DELETE FROM oauth_authorization_codes;
