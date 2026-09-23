ALTER TABLE script_runs
    DROP COLUMN IF EXISTS failure_cause,
    DROP COLUMN IF EXISTS heartbeat_at,
    DROP COLUMN IF EXISTS claimed_at,
    DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS reclaims;
