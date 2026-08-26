-- Per-(connection, method, path) API gateway rules for a database-managed
-- persona. Without this column a persona edited in the portal was written
-- back with its file-configured api_routes silently dropped, and the
-- endpoints those rules denied became callable (issue #1479).
--
-- Same shape as persona.APIRouteRule in JSON: an array of
-- {connection, methods, paths, action}.

ALTER TABLE persona_definitions
    ADD COLUMN IF NOT EXISTS api_routes JSONB NOT NULL DEFAULT '[]';
