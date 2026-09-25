# Scratch Catalog

Everything the platform creates in Trino goes into a **scratch catalog**: the
tables [registered](registered-tables.md) over uploaded and exported files, and
the tables and view of every [inbound webhook source](webhooks.md). This page
is every requirement that catalog has to meet, in one place. The pages for the
features that use it link here rather than repeating it.

## Which setting each feature needs

| Setting | Registered tables | Webhook sources |
|---|---|---|
| A Trino connection with a `scratch:` target | Required | Required |
| `read_only: false` on that connection | Required | Required |
| The catalog reads the store the files are in | Required | Required (the managed-resources store) |
| `hive.recursive-directories=false` | Required | Required |
| `hive.timestamp-precision=MICROSECONDS` | Required for Parquet with timestamps | Required |
| `hive.allow-register-partition-procedure=true` | Not used | Required |
| A Trino identity allowed DDL only on this catalog | Strongly recommended | Strongly recommended |

## The Trino connection

A table can only be created on a Trino connection that names a **scratch
target**: the catalog and schema the platform writes into.

```yaml
toolkits:
  trino:
    enabled: true
    instances:
      warehouse:
        host: trino.example.com
        user: "${TRINO_READONLY_USER}"
        catalog: warehouse
        read_only: true
      scratch:
        host: trino.example.com
        # A DISTINCT Trino identity, whose access-control rules allow DDL only
        # on the scratch catalog. See "The Trino identity" below.
        user: "${TRINO_SCRATCH_USER}"
        password: "${TRINO_SCRATCH_PASSWORD}"
        catalog: scratch
        schema: uploads
        read_only: false
        scratch:
          catalog: scratch
          schema: uploads
```

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

## The catalog

The catalog is a Hive connector over an object store. A file metastore is
enough:

```properties
# etc/catalog/scratch.properties
connector.name=hive
hive.metastore=file
hive.metastore.catalog.dir=s3://<bucket>/trino-metastore/
hive.recursive-directories=false
hive.timestamp-precision=MICROSECONDS
hive.allow-register-partition-procedure=true
fs.native-s3.enabled=true
s3.endpoint=<your object store endpoint>
s3.path-style-access=true
s3.region=us-east-1
s3.aws-access-key=${ENV:SCRATCH_S3_KEY}
s3.aws-secret-key=${ENV:SCRATCH_S3_SECRET}
```

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

This enables `system.register_partition` and `system.unregister_partition`,
which place one partition of a table at an explicit location. Without it both
fail with *"register_partition procedure is disabled"*.

A webhook source stores each compacted window as a managed resource. A managed
resource sits in a directory of its own, so each window's partition has to be
registered at that resource's directory, which only this procedure can do.
Creating a source on a catalog without the setting is refused with that
reason. Registered tables do not use it.

## The Trino identity

What keeps the platform's tables off the warehouse is the Trino identity the
scratch connection authenticates as. The platform's `read_only` flag is a
statement-prefix denylist evaluated per connection. Nothing in the toolkit
restricts a catalog or a schema, and `catalog`/`schema` on a connection are
session defaults rather than bounds. A scratch connection that authenticates as
the same Trino user as the warehouse connection can write to the warehouse.

Give the scratch connection its own Trino user, with access-control rules that
allow DDL only on the scratch catalog. The rules must also allow that user to
call the catalog's `system` procedures the features use:
`sync_partition_metadata`, `register_partition` and `unregister_partition` for
webhook sources.

## Related

- [Registered Tables](registered-tables.md): files registered as tables.
- [Inbound Webhooks](webhooks.md): sources whose events land as a table.
- [Threat Model](../security/threat-model.md): what the scratch schema exposes.
