-- Pre-create databases used by the mcp-test and api-test dev fixtures.
-- Mounted into acme-dev-postgres at /docker-entrypoint-initdb.d/. Postgres
-- runs everything in that directory once, on the very first container
-- startup against a fresh data volume. `make dev-down` removes the
-- volume (`docker compose down -v`), so the next `make dev` always
-- starts clean and re-applies this file.
--
-- Note on grants: the platform's POSTGRES_USER ("platform") owns these
-- databases. The fixture containers connect with the same credentials
-- (see docker-compose.yml). If the fixtures ever move to a dedicated
-- role, this file is the right place to CREATE ROLE + GRANT.

CREATE DATABASE mcp_test;
CREATE DATABASE apitest;
