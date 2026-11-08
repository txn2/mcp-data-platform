-- 000235: static sites served by the platform (#2518) and endpoints that serve
-- browser pages (#2517).
--
-- A site has environments, each pointing at one immutable release: a file set
-- under its own object-storage prefix, listed in site_release_files. Publishing
-- adds a release; promote and rollback only move an environment's pointer, and
-- site_deployments records every move.
CREATE TABLE IF NOT EXISTS sites (
    id          TEXT        NOT NULL PRIMARY KEY,
    name        TEXT        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    owner_email TEXT        NOT NULL,
    created_by  TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS site_releases (
    id              TEXT        NOT NULL PRIMARY KEY,
    site_id         TEXT        NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    number          INT         NOT NULL CHECK (number > 0),
    source          TEXT        NOT NULL DEFAULT '',
    note            TEXT        NOT NULL DEFAULT '',
    file_count      INT         NOT NULL,
    total_bytes     BIGINT      NOT NULL,
    -- manifest_sha256 fixes the file set, which is what an approval names.
    manifest_sha256 TEXT        NOT NULL,
    redirects       JSONB       NOT NULL DEFAULT '[]',
    created_by      TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (site_id, number)
);

CREATE TABLE IF NOT EXISTS site_release_files (
    release_id   TEXT   NOT NULL REFERENCES site_releases(id) ON DELETE CASCADE,
    path         TEXT   NOT NULL,
    sha256       TEXT   NOT NULL,
    content_type TEXT   NOT NULL,
    size         BIGINT NOT NULL,
    gzip_size    BIGINT NOT NULL DEFAULT 0,
    brotli_size  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (release_id, path)
);

CREATE TABLE IF NOT EXISTS site_environments (
    site_id           TEXT        NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name              TEXT        NOT NULL,
    release_number    INT,
    requires_approval BOOLEAN     NOT NULL DEFAULT FALSE,
    updated_by        TEXT        NOT NULL DEFAULT '',
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (site_id, name)
);

CREATE TABLE IF NOT EXISTS site_deployments (
    id           BIGSERIAL   PRIMARY KEY,
    site_id      TEXT        NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    environment  TEXT        NOT NULL,
    action       TEXT        NOT NULL CHECK (action IN ('publish', 'promote', 'rollback', 'application')),
    from_release INT         NOT NULL DEFAULT 0,
    to_release   INT         NOT NULL,
    actor        TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_site_deployments_env ON site_deployments (site_id, environment, id DESC);

-- A hostname maps to one site environment or one application. It routes only
-- once verified_at is set.
CREATE TABLE IF NOT EXISTS site_domains (
    hostname       TEXT        NOT NULL PRIMARY KEY,
    site_id        TEXT        REFERENCES sites(id) ON DELETE CASCADE,
    environment    TEXT        NOT NULL DEFAULT '',
    application_id TEXT        REFERENCES applications(id) ON DELETE CASCADE,
    token          TEXT        NOT NULL,
    verified_at    TIMESTAMPTZ,
    verified_by    TEXT        NOT NULL DEFAULT '',
    created_by     TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((site_id IS NOT NULL AND environment <> '' AND application_id IS NULL)
        OR (site_id IS NULL AND environment = '' AND application_id IS NOT NULL))
);

-- The automatic certificates of verified domains. data is sealed, bound to key.
CREATE TABLE IF NOT EXISTS site_certificates (
    key        TEXT        NOT NULL PRIMARY KEY,
    data       TEXT        NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The one-time codes that hand a signed-in person to a page origin. Only a
-- code's hash is kept, so a row cannot be replayed as a code.
CREATE TABLE IF NOT EXISTS page_handoffs (
    code_hash  TEXT        NOT NULL PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_page_handoffs_expiry ON page_handoffs (expires_at);

-- An endpoint opts in to serving a browser page (#2517).
ALTER TABLE script_endpoints ADD COLUMN IF NOT EXISTS response_mode TEXT NOT NULL DEFAULT 'api'
    CHECK (response_mode IN ('api', 'page'));
ALTER TABLE script_endpoints ADD COLUMN IF NOT EXISTS page_cdns TEXT[] NOT NULL DEFAULT '{}';

-- One environment of a site can be a part of an application, which pins the
-- release it serves.
ALTER TABLE application_parts DROP CONSTRAINT IF EXISTS application_parts_kind_check;
ALTER TABLE application_parts ADD CONSTRAINT application_parts_kind_check
    CHECK (kind IN ('script', 'asset', 'source', 'site'));
ALTER TABLE application_release_pins DROP CONSTRAINT IF EXISTS application_release_pins_kind_check;
ALTER TABLE application_release_pins ADD CONSTRAINT application_release_pins_kind_check
    CHECK (kind IN ('script', 'asset', 'site'));

-- A promotion to an environment that requires approval is a request in the
-- Approvals queue, fixed by the release's manifest digest.
ALTER TABLE approval_requests ALTER COLUMN script_id DROP NOT NULL;
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS site_release_id TEXT REFERENCES site_releases(id) ON DELETE CASCADE;
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS target TEXT NOT NULL DEFAULT '';
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_subject_kind_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_kind_check
    CHECK (subject_kind IN ('script_version', 'site_release'));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_purpose_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_purpose_check
    CHECK (purpose IN ('form_public', 'site_promote'));
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_present
    CHECK ((subject_kind = 'script_version' AND script_id IS NOT NULL)
        OR (subject_kind = 'site_release' AND site_release_id IS NOT NULL));
CREATE UNIQUE INDEX IF NOT EXISTS idx_approval_requests_one_pending_site
    ON approval_requests (site_release_id, target) WHERE status = 'pending' AND site_release_id IS NOT NULL;
