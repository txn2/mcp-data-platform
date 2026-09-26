# Scratch Catalog

Everything the platform creates in Trino goes into a **scratch catalog**: the
tables [registered](registered-tables.md) over uploaded, exported and
extracted files, and the tables and view of every
[inbound webhook source](webhooks.md). This page is one complete setup that
makes every scratch feature work: the Trino identities, the catalog, the
access-control rules, the platform connections, and how to check the result.
The pages for the features that use it link here rather than repeating it.

## Which setting each feature needs

| Setting | Registered tables (CSV, JSON lines, Parquet) | Archive extraction, then registration | Webhook sources |
|---|---|---|---|
| A Trino connection with a `scratch:` target and `read_only: false` | Required | Required | Required |
| The catalog reads the store the files are in | Required | Required (the managed-resources store) | Required (the managed-resources store) |
| `hive.recursive-directories=false` | Required | Required | Required |
| `hive.timestamp-precision=MICROSECONDS` | Required for Parquet with timestamps | Required for Parquet with timestamps | Required |
| `hive.allow-register-partition-procedure=true` | Not used | Not used | Required |
| A `procedures` rule letting the scratch identity call the partition procedures | Not used | Not used | Required |
| A Trino identity allowed DDL only on this catalog | Strongly recommended | Strongly recommended | Strongly recommended |

Archive extraction (`manage_resource action=extract`) writes each member out as
a managed resource; registering one of those is an ordinary registration over
a managed resource, so it needs what registered tables need, on a catalog that
reads the managed-resources store.

## The Trino identities

What keeps the platform's tables off the warehouse is the Trino identity the
scratch connection authenticates as. The platform's `read_only` flag is a
statement-prefix denylist evaluated per connection. Nothing in the toolkit
restricts a catalog or a schema, and `catalog`/`schema` on a connection are
session defaults rather than bounds. A scratch connection that authenticates as
the same Trino user as the warehouse connection can write to the warehouse.

Use two identities:

- **`mcp-server`**, the read connection. Read-only everywhere it queries,
  including the scratch catalog, so that readers can query registered tables
  and `webhook_*` views.
- **`mcp-scratch`**, the scratch connection. `all` on the scratch catalog,
  read-only on `system`, and nothing else, plus permission to call the three
  partition procedures on the scratch catalog. This is what keeps the
  platform's DDL off the warehouse.

The names are examples. The access-control rules below match users by name, so
use the names your coordinator authenticates.

## The catalog

The catalog is a Hive connector over an object store. A file metastore is
enough. Every key below is required:

```properties
# etc/catalog/scratch.properties
connector.name=hive
hive.metastore=file
hive.metastore.catalog.dir=s3://<scratch-bucket>/trino-metastore/
hive.recursive-directories=false
hive.timestamp-precision=MICROSECONDS
hive.allow-register-partition-procedure=true
fs.native-s3.enabled=true
s3.endpoint=<the store holding BOTH managed resources and portal assets>
s3.path-style-access=true
s3.region=us-east-1
s3.aws-access-key=${ENV:SCRATCH_S3_KEY}
s3.aws-secret-key=${ENV:SCRATCH_S3_SECRET}
```

**Trino reads catalog properties at startup. After changing one, restart the
coordinator and every worker.** A worker still running the old properties
answers with them. Access-control rules are different: they are re-read on
`security.refresh-period` (see [Access control](#access-control)).

Trino needs its own credentials to the bucket. They are separate from the
platform's S3 connection credentials and are configured on the Trino cluster,
not in the platform.

### One store per catalog

A Hive catalog reads its metastore and every one of its tables through one S3
client. A catalog can therefore only hold tables over objects in the store its
`s3.endpoint` names. The platform writes files to two places:

- Managed resources go to the bucket of `resources.managed.s3_connection`.
- Portal assets go to the bucket of `portal.s3_connection`.

Most deployments point both at one object store, and one catalog serves both.
A deployment that splits them across two stores needs a catalog over each, on
two scratch connections, and a table has to be created on the connection whose
catalog reads its file.

A webhook source writes its raw segments and its compacted windows into the
managed-resources bucket, so its connection's catalog must read the
managed-resources store. Creating a source checks this: the platform creates
the source's tables and reads through them, and refuses the source, naming the
connection and the bucket, when the catalog cannot.

### `hive.recursive-directories=false`

A registered table's external location is the directory a file sits in, and a
Hive table reads every non-hidden object in that directory. With recursive
directories off, it reads the objects directly in that directory and nothing
in a directory below it, which is what the platform's check for other files
beside the one being registered assumes.

### `hive.timestamp-precision=MICROSECONDS`

This is the precision a Parquet file's timestamps read back exactly at. The
connector's default, `MILLISECONDS`, truncates a microsecond timestamp on every
read, and it refuses a column declared at any precision but the catalog's.
A registration declares every timestamp column `TIMESTAMP(6)`, so on a catalog
left at the default the `CREATE TABLE` for a Parquet file with a timestamp
column fails with *"Incorrect timestamp precision for timestamp(6); the
configured precision is MILLISECONDS"*. CSV and JSON-lines registrations
declare no timestamp columns and are not affected.

A webhook source's tables declare `received_at` and `landed_at` as
`timestamp(6)`, so a source needs this setting.

### `hive.allow-register-partition-procedure=true`

This enables `system.register_partition`, which places one partition of a table
at an explicit location. Without it, `register_partition` fails with
*"register_partition procedure is disabled"*. `unregister_partition` and
`sync_partition_metadata` do not depend on it.

A webhook source stores each compacted window as a managed resource. A managed
resource sits in a directory of its own, so each window's partition has to be
registered at that resource's directory, which only this procedure can do.
Creating a source on a catalog without the setting is refused with that
reason. Registered tables do not use it.

## Access control

These rules use Trino's
[file-based access control](https://trino.io/docs/current/security/file-system-access-control.html).

```properties
# etc/access-control.properties
access-control.name=file
security.config-file=/etc/trino/rules.json
security.refresh-period=30s
```

```json
{
  "catalogs": [
    { "user": "admin",       "catalog": ".*",        "allow": "all" },
    { "user": "mcp-scratch", "catalog": "scratch",   "allow": "all" },
    { "user": "mcp-scratch", "catalog": "system",    "allow": "read-only" },
    { "user": "mcp-server",  "catalog": "warehouse", "allow": "read-only" },
    { "user": "mcp-server",  "catalog": "scratch",   "allow": "read-only" },
    { "user": "mcp-server",  "catalog": "system",    "allow": "read-only" }
  ],
  "functions": [
    { "catalog": "system", "schema": "builtin", "privileges": ["EXECUTE", "GRANT_EXECUTE"] },
    { "user": "admin", "privileges": ["EXECUTE", "GRANT_EXECUTE", "OWNERSHIP"] }
  ],
  "procedures": [
    { "user": "admin", "catalog": ".*", "schema": ".*", "procedure": ".*", "privileges": ["EXECUTE"] },
    {
      "user": "mcp-scratch",
      "catalog": "scratch",
      "schema": "system",
      "procedure": "(sync_partition_metadata|register_partition|unregister_partition)",
      "privileges": ["EXECUTE"]
    }
  ]
}
```

Which rule serves which feature:

| Rule | What it is for |
|---|---|
| `mcp-scratch` `all` on `scratch` | Creating, replacing and dropping registered tables and a webhook source's tables and view. |
| `mcp-scratch` `read-only` on `system` | The metadata queries a connection makes. |
| `mcp-server` `read-only` on `warehouse` | Ordinary queries. |
| `mcp-server` `read-only` on `scratch` | Querying registered tables and `webhook_*` views. A webhook view is `SECURITY INVOKER`, so the reader needs to read the tables beneath it, and this rule covers them. |
| `mcp-server` `read-only` on `system` | The metadata queries a connection makes. |
| The `procedures` rule for `mcp-scratch` | Webhook sources: `sync_partition_metadata`, `register_partition` and `unregister_partition` on the scratch catalog's `system` schema. |
| The `functions` rules | Built-in functions for everyone, and every function for `admin`. |

Two defaults in Trino's file-based access control decide what these sections
must hold:

- **A catalog rule does not grant procedures.** `"allow": "all"` on a catalog
  does not let that user call the catalog's procedures, and when the rules file
  has no `procedures` section only procedures in `system.builtin` can run. A
  scratch identity with `all` on the catalog and no `procedures` rule is refused
  with *"Access Denied: Cannot execute procedure
  scratch.system.unregister_partition"*.
- **When the rules file has no `functions` section, only functions in
  `system.builtin` can run.** A table function a deployment uses needs a rule
  of its own. For example, the OpenSearch connector's `raw_query` needs:

  ```json
  { "user": "mcp-server", "catalog": "opensearch", "schema": "system", "function": "raw_query", "privileges": ["EXECUTE"] }
  ```

  If your rules already carry a `functions` section, keep it when you add the
  `procedures` section.

## The platform connections

A table can only be created on a Trino connection that names a **scratch
target**: the catalog and schema the platform writes into.

```yaml
toolkits:
  trino:
    enabled: true
    instances:
      warehouse:
        host: trino.example.com
        user: "${TRINO_READONLY_USER}"     # mcp-server
        catalog: warehouse
        read_only: true
      scratch:
        host: trino.example.com
        user: "${TRINO_SCRATCH_USER}"      # mcp-scratch
        password: "${TRINO_SCRATCH_PASSWORD}"
        catalog: scratch
        schema: uploads
        read_only: false
        scratch:
          catalog: scratch
          schema: uploads
```

`resources.managed.s3_connection` and `portal.s3_connection` must name stores
the catalog's `s3.endpoint` reaches; see
[One store per catalog](#one-store-per-catalog).

Both connections above reach the same coordinator over HTTPS. On a coordinator
that speaks plain HTTP, each connection has to say so with `ssl: false` and its
port: a connection that never mentions `ssl` is assumed to be HTTPS on 443
unless it is the one named by `default:` or its host is localhost. See
[`ssl`](configuration.md#trino).

A connection with no `scratch:` block cannot hold a table, and the surfaces do
not offer one on it. Both keys are required: a block naming only one is ignored
with a warning, because a table built on it would fail at the DDL.

`read_only: false` on the scratch connection is not decoration either. A
scratch target says *where* a table is written; it grants nothing. The
statement that creates the table is write SQL, so a `read_only: true`
connection refuses it however its target is configured, and such a connection
is not offered, for the same reason one with no target is not. Naming it
directly anyway (through `manage_table`, a webhook source, or a form built
before an administrator flipped the flag) is refused with **400** and the
sentence *"this connection is read-only, so a table cannot be created on it"*.
It is not a 500: the connection is working exactly as configured.

## Checking the setup

The platform checks the scratch connection for you. Test it in the portal
(**Admin > Connections**, then **Test**) or over the admin API:

```bash
curl -X POST https://platform.example.com/api/v1/admin/connection-instances/trino/scratch/test \
  -H "Authorization: Bearer $ADMIN_KEY"
```

On a connection that accepts writes and names a scratch target, the test runs
`SELECT 1` and then calls each partition procedure on a table that does not
exist. Trino checks access control before a procedure runs, and the Hive
connector checks its property before it looks up the table, so an answer of
*"Table ... not found"* means both are in place, and nothing is changed. The
test answers:

- **200**, with *"the scratch catalog scratch allows this connection to call
  sync_partition_metadata, register_partition, unregister_partition"*, when the
  catalog and the rules are ready for every scratch feature.
- **503**, naming every missing setting at once, with Trino's own words in
  `error`, when they are not.

To check by hand, run each of these and compare the answer:

| As | Statement | Expected answer |
|---|---|---|
| `mcp-scratch` | `SHOW CATALOGS` | `scratch` and `system`, and nothing else |
| `mcp-scratch` | `CALL scratch.system.sync_partition_metadata('uploads', 'no_such_table', 'ADD')` | *"Table 'uploads.no_such_table' not found"*. *"Access Denied"* means the `procedures` rule is missing. |
| `mcp-scratch` | `CALL scratch.system.register_partition('uploads', 'no_such_table', ARRAY['dt'], ARRAY['x'], 's3://<scratch-bucket>/x/')` | *"Table 'uploads.no_such_table' not found"*. *"register_partition procedure is disabled"* means the catalog property is missing. |
| `mcp-server` | `DROP TABLE scratch.uploads.<any table>` | *"Access Denied: Cannot drop table ..."* |

Then exercise the features:

1. Register a CSV file, and a Parquet file with a timestamp column, with
   `manage_table register` on the scratch connection. Query each through the
   read connection; each returns its rows, and the timestamps read back with
   their microseconds.
2. Create a webhook source on the scratch connection. It is created. A signed
   POST to `/hooks/<name>` answers **202**, and the event is in
   `webhook_<name>` straight away.
3. After the source's window ends and `webhooks.compactor.grace` has passed,
   the source's status shows `last_compacted_window` at that window, and a
   duplicate event posted twice is returned once.

## Refusals and the setting that fixes each

| Refusal | Where | Fix |
|---|---|---|
| *"this connection has no scratch catalog and schema configured"* | Registering a table, creating a source | Add a `scratch:` block with both `catalog` and `schema` to the connection. |
| *"this connection is read-only, so a ... table cannot be created on it"* | Registering a table, creating a source | Set `read_only: false` on the scratch connection. |
| *"the scratch catalog of connection X could not read the managed-resources bucket B"* | Creating a source | Point the catalog's `s3.endpoint` at the store holding `resources.managed.s3_connection`'s bucket, with credentials that read it. |
| *"Incorrect timestamp precision for timestamp(6); the configured precision is MILLISECONDS"* | Registering Parquet with timestamps, creating a source | Set `hive.timestamp-precision=MICROSECONDS` and restart the coordinator and every worker. |
| *"the scratch catalog does not allow register_partition; set hive.allow-register-partition-procedure=true"* | Creating a source, the connection test | Set the property and restart the coordinator and every worker. |
| *"the Trino user of connection X may not EXECUTE scratch.system.P; ... add a procedures rule"* | Creating a source, the connection test | Add the `procedures` rule from [Access control](#access-control) for the connection's Trino user. |
| *"Access Denied: Cannot create table ..."* | Registering a table, creating a source | Give the scratch identity `all` on the scratch catalog. |

## Related

- [Registered Tables](registered-tables.md): files registered as tables.
- [Inbound Webhooks](webhooks.md): sources whose events land as a table.
- [Admin API: Test a Connection Instance](admin-api.md#test-a-connection-instance): the check above.
- [Threat Model](../security/threat-model.md): what the scratch schema exposes.
