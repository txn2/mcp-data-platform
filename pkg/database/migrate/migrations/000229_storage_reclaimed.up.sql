-- The orphans the last reconcile deleted, per purpose (#2470).
ALTER TABLE storage_reconciles ADD COLUMN IF NOT EXISTS reclaimed BIGINT NOT NULL DEFAULT 0;
