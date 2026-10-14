-- The GraphQL fixture's data: the schema PostGraphile (the `graphql` service in
-- dev/docker-compose.yml) publishes as the dev stack's GraphQL endpoint, which
-- the graphql connection kind's acceptance criteria run against.
--
-- dev/start.sh applies this to the gql_fixture database before the service
-- starts, on every `make dev`: the schema is dropped and rebuilt, so the
-- fixture is the same on every run and a criterion that wrote to it leaves
-- nothing behind for the next.
--
-- What the criteria need from it:
--   appConfig { appVersion analyticsConfig { enabled } authConfig { tokenAuthEnabled } }
--     a field with no arguments returning an object, so the platform's schema
--     walk treats appConfig as a namespace (query:appConfig and
--     query:appConfig.analyticsConfig), and three levels for max_query_depth.
--   findDataset(code: String!)
--     a lookup whose resolver refuses a malformed code with an error inside an
--     HTTP 200, the failure the platform must record as one.
--   allDatasets
--     a Relay connection over 250 rows, which paginate walks a page at a time
--     and an aliased read pushes past the result budget.
--   createDataset / updateDatasetByCode / deleteDatasetByCode / ...
--     mutations, which read_only connections and persona method rules refuse.

DROP SCHEMA IF EXISTS gqlfixture CASCADE;
CREATE SCHEMA gqlfixture;

CREATE TYPE gqlfixture.analytics_config AS (enabled boolean);
COMMENT ON TYPE gqlfixture.analytics_config IS 'Analytics settings.';

CREATE TYPE gqlfixture.auth_config AS (token_auth_enabled boolean);
COMMENT ON TYPE gqlfixture.auth_config IS 'Authentication settings.';

CREATE TYPE gqlfixture.app_config AS (
    app_version      text,
    analytics_config gqlfixture.analytics_config,
    auth_config      gqlfixture.auth_config
);
COMMENT ON TYPE gqlfixture.app_config IS 'The fixture''s configuration.';

CREATE FUNCTION gqlfixture.app_config() RETURNS gqlfixture.app_config
LANGUAGE sql STABLE AS $$
    SELECT ROW(
        'gql-fixture-1',
        ROW(true)::gqlfixture.analytics_config,
        ROW(false)::gqlfixture.auth_config
    )::gqlfixture.app_config
$$;
COMMENT ON FUNCTION gqlfixture.app_config() IS 'The fixture''s configuration.';

CREATE TABLE gqlfixture.platforms (
    code text PRIMARY KEY,
    name text NOT NULL
);
COMMENT ON TABLE gqlfixture.platforms IS 'A system datasets are held in.';

INSERT INTO gqlfixture.platforms (code, name) VALUES
    ('warehouse', 'Warehouse'),
    ('lake', 'Data lake'),
    ('crm', 'CRM');

CREATE TABLE gqlfixture.datasets (
    code        text PRIMARY KEY,
    name        text NOT NULL,
    platform    text NOT NULL REFERENCES gqlfixture.platforms (code),
    description text,
    row_count   bigint NOT NULL DEFAULT 0
);
COMMENT ON TABLE gqlfixture.datasets IS 'A dataset the fixture catalogs.';
CREATE INDEX ON gqlfixture.datasets (platform);

INSERT INTO gqlfixture.datasets (code, name, platform, description, row_count)
SELECT 'acme-dataset-' || lpad(i::text, 4, '0'),
       'dataset_' || i,
       (ARRAY['warehouse', 'lake', 'crm'])[1 + i % 3],
       'Fixture dataset number ' || i || '.',
       i * 1000
FROM generate_series(1, 250) AS i;

-- find_dataset reads one dataset by its code. A code not of the fixture's
-- form is refused by the resolver, which PostGraphile reports in the errors
-- array of an HTTP 200; a well-formed code no dataset has reads null.
CREATE FUNCTION gqlfixture.find_dataset(code text) RETURNS gqlfixture.datasets
LANGUAGE plpgsql STABLE STRICT AS $$
DECLARE
    hit gqlfixture.datasets;
BEGIN
    IF code !~ '^acme-dataset-[0-9]{4}$' THEN
        RAISE EXCEPTION 'not a dataset code: %', code;
    END IF;
    SELECT * INTO hit FROM gqlfixture.datasets d WHERE d.code = find_dataset.code;
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;
    RETURN hit;
END
$$;
COMMENT ON FUNCTION gqlfixture.find_dataset(text) IS 'Read one dataset by its code.';
