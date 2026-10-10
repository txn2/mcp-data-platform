-- Bucket capacity and integrity (#1899). One row per bucket the platform owns
-- and purpose it holds there: the last full listing's totals, moved by every
-- put and delete the platform makes in between, so every replica reports the
-- same figure and an upload shows without waiting for the next listing.
CREATE TABLE IF NOT EXISTS storage_usage (
    bucket     TEXT NOT NULL,
    purpose    TEXT NOT NULL,
    bytes      BIGINT NOT NULL DEFAULT 0,
    objects    BIGINT NOT NULL DEFAULT 0,
    listed_at  TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bucket, purpose)
);

-- What the last reconcile found per purpose: objects under a platform prefix
-- no row references, and rows whose object is gone. Counts only, never keys.
CREATE TABLE IF NOT EXISTS storage_reconciles (
    purpose       TEXT PRIMARY KEY,
    orphaned      BIGINT NOT NULL DEFAULT 0,
    dangling      BIGINT NOT NULL DEFAULT 0,
    reconciled_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The last full listing: how long it took and how many objects it read. One
-- row, replaced by each listing.
CREATE TABLE IF NOT EXISTS storage_scans (
    id               SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    duration_seconds DOUBLE PRECISION NOT NULL,
    objects          BIGINT NOT NULL,
    finished_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
