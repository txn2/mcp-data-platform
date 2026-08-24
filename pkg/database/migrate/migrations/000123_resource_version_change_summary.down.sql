-- Reverse 000123. A downgraded deployment is back to a resource version trail
-- that cannot say why a revision was written, so a correction the platform made
-- reads as an upload again.
ALTER TABLE resource_versions
    DROP COLUMN IF EXISTS change_summary;
