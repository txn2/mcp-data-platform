ALTER TABLE connection_auth_alerts
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS signed_assertion;
