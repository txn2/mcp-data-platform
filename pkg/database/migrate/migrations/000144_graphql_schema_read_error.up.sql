-- The refusal a graphql connection's last schema read met, kept with the
-- schema it was reported beside (#1703).
--
-- A re-read the endpoint refuses leaves the schema a connection holds in
-- place and reports the refusal next to it. The refusal was held in the
-- memory of the replica that ran the read: another replica answered the
-- same connection with no error, and a restart forgot it while the schema,
-- its hash and fetched_at survived in this table.
--
-- read_error is written by a refused read and cleared by a read or an
-- upload that installs a schema. It lives on the schema row because it
-- describes that row: a connection with no stored schema has no row, and
-- every replica reads such a connection's endpoint itself when it starts.
ALTER TABLE graphql_connection_schemas
    ADD COLUMN IF NOT EXISTS read_error TEXT NOT NULL DEFAULT '';
