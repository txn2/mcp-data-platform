-- What an export recorded about the content of a resource version (#2057).
--
-- An export cut at a row or page limit and written anyway, under
-- on_truncation "warn", records the cut on the version it lands: truncated,
-- limit_applied, limit_source and limit_unit. The resource page and its version
-- history read the head version's to say the file is incomplete, and a restore
-- carries the restored version's forward with its bytes. A version nothing
-- described, which is every version written before this, carries '{}'.
ALTER TABLE resource_versions
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}';
