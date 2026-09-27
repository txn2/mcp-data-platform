-- A script's tile (#1909): its flow diagram (#1906) drawn by the tile worker
-- for the scripts listing's grid view. One row per script, keyed by the script
-- and naming the version the stored tile was drawn from, so saving a version
-- owes a new tile. The row outlives the script on purpose: the worker removes
-- a deleted script's stored tiles and then the row, so no object is left behind.
CREATE TABLE IF NOT EXISTS script_tiles (
    script_id      UUID        PRIMARY KEY,
    version        INTEGER     NOT NULL DEFAULT 0,
    s3_key         TEXT        NOT NULL DEFAULT '',
    renderer       INTEGER     NOT NULL DEFAULT 0,
    failure        TEXT        NOT NULL DEFAULT '',
    failed_version INTEGER     NOT NULL DEFAULT 0,
    claimed_until  TIMESTAMPTZ,
    attempts       INTEGER     NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
