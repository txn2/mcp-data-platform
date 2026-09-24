-- The content hash of each resource version (#1862).
--
-- A bulk upload that re-sends a folder has to tell a file it already holds
-- from one that changed: identical bytes are skipped, different bytes become
-- the next version. The hash is taken while the upload streams to storage, so
-- a version written before this migration has none, and the first re-upload
-- of such a file is recorded as a version that carries one.
ALTER TABLE resource_versions
    ADD COLUMN IF NOT EXISTS content_sha256 TEXT;
