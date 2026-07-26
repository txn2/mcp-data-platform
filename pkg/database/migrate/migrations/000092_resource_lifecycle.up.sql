-- 000092: managed-resource lifecycle (#1014)
--
-- Until now an uploaded resource was frozen and unaccountable: content could
-- only be replaced by delete-plus-re-upload (which mints a new id and breaks
-- every mcp:resource:<id> citation and prompt attachment pointing at it), and
-- reads were logged at DEBUG only, so a curator could not tell a template used
-- daily from one dead since upload.
--
-- Three changes:
--
--   1. resource_versions records one row per content revision. The live
--      `resources` row remains the head — its s3_key, mime_type, size_bytes and
--      updated_at always describe the current content — and each version row
--      carries the blob that revision wrote, at its own per-version S3 key
--      (resources/<scope>/<scope_id>/<id>/v<N>/<filename>). Filename is NOT a
--      version column: the canonical URI embeds the filename and is required to
--      stay stable across a revision (that is the point of the feature), so the
--      filename is fixed at create time and every version of a resource shares
--      it. restored_from names the version a restore re-promoted, so the trail
--      distinguishes a fresh upload from a rollback.
--
--      Retention is bounded (default 10, resources.managed.max_versions): a
--      revision past the cap deletes the oldest version rows and their S3
--      objects. ON DELETE CASCADE ties the trail to the resource, so deleting a
--      resource takes its version rows with it (the blobs are removed by the
--      delete handler).
--
--   2. Existing resources become version 1 with their current blob. This is a
--      metadata backfill only — no blob is copied or moved, the v1 row points at
--      the s3_key the resource already has — so a deployment upgrading into this
--      migration has a complete version list on first view rather than an empty
--      panel next to content that plainly exists.
--
--   3. resources.last_read_at plus the audit-side index back usage stats. The
--      read recorder stamps last_read_at on every audited read, which is what
--      makes "sort the admin table by last read" an ORDER BY instead of a
--      cross-table rollup of the audit log; the per-surface 30/90-day counts
--      come from the resource_read audit events themselves. The partial
--      expression index keeps that rollup off a full partition scan, exactly as
--      idx_audit_logs_prompt_serve does for prompt usage (000085).

CREATE TABLE IF NOT EXISTS resource_versions (
    resource_id    TEXT        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version        INTEGER     NOT NULL,
    mime_type      TEXT        NOT NULL,
    size_bytes     BIGINT      NOT NULL,
    s3_key         TEXT        NOT NULL,
    uploader_sub   TEXT        NOT NULL DEFAULT '',
    uploader_email TEXT        NOT NULL DEFAULT '',
    restored_from  INTEGER,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (resource_id, version)
);

-- Newest-first is the only order the version list and the next-version lookup
-- ask for; the primary key already covers (resource_id, version) ascending.
CREATE INDEX IF NOT EXISTS idx_resource_versions_resource
    ON resource_versions (resource_id, version DESC);

INSERT INTO resource_versions
    (resource_id, version, mime_type, size_bytes, s3_key,
     uploader_sub, uploader_email, created_at)
SELECT id, 1, mime_type, size_bytes, s3_key, uploader_sub, uploader_email, created_at
  FROM resources
ON CONFLICT (resource_id, version) DO NOTHING;

ALTER TABLE resources ADD COLUMN IF NOT EXISTS last_read_at TIMESTAMPTZ;

-- NULLS LAST matches the "never read" end of the sort: a resource nobody has
-- opened sorts to the bottom of a most-recently-read ordering, which is where
-- the curator looking for dead weight goes to find it.
CREATE INDEX IF NOT EXISTS idx_resources_last_read
    ON resources (last_read_at DESC NULLS LAST);

CREATE INDEX IF NOT EXISTS idx_audit_logs_resource_read
    ON audit_logs ((parameters->>'resource_id'))
    WHERE event_kind = 'resource_read';
