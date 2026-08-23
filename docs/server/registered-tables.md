# Registered Tables

A file that reaches the platform - a CSV a person uploaded as a managed
resource, or one `trino_export` wrote as a portal asset - can be registered as
a table on a Trino connection and joined to warehouse tables from then on.

Nothing is copied. The registration creates an external table over the
directory the file already sits in, so the table reads whatever the object
holds now: a vendor drop that overwrites its file changes the query result on
the next run with no further action.

## What it is for

Getting a short list of outside keys into a report is otherwise awkward. Trino
has no client-side upload, and an agent emitting `INSERT ... VALUES` batches for
a fifty-thousand-row vendor CSV is slow and burns context. The join is the easy
part; loading is the gap.

A few hundred keys need no table at all - they join inline with
`JOIN (VALUES ('a'),('b')) AS t(id)` through `trino_query` on a read-only
connection. Registration is for the file that is too big for that.

## What an operator has to configure

Registration is available on a Trino connection that names a **scratch target**:
the catalog and schema registrations are written into.

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
        # on the scratch catalog. See "What the scratch schema is" below.
        user: "${TRINO_SCRATCH_USER}"
        password: "${TRINO_SCRATCH_PASSWORD}"
        catalog: scratch
        schema: uploads
        read_only: false
        scratch:
          catalog: scratch
          schema: uploads
```

A connection with no `scratch:` block cannot hold a registration, and the
surfaces do not offer one on it. Both keys are required: a block naming only
one is ignored with a warning, because a registration built on it would fail at
the DDL.

The catalog itself is a Hive connector over the same object store the
platform's managed resources and portal assets live in. A file metastore is
enough:

```properties
# etc/catalog/scratch.properties
connector.name=hive
hive.metastore=file
hive.metastore.catalog.dir=s3://<bucket>/trino-metastore/
hive.recursive-directories=false
fs.native-s3.enabled=true
s3.endpoint=<your object store endpoint>
s3.path-style-access=true
s3.region=us-east-1
s3.aws-access-key=${ENV:SCRATCH_S3_KEY}
s3.aws-secret-key=${ENV:SCRATCH_S3_SECRET}
```

Trino needs its own credentials to the bucket. They are separate from the
platform's S3 connection credentials and are configured on the Trino cluster,
not here.

## Registering

**In the portal.** Open a CSV resource or a CSV asset. A *Query as a table*
section offers the connections you can reach that can hold one, and shows what
is already registered with the columns each table has.

**From a session.** `manage_table` carries three actions, on either kind of
stored file:

```
manage_table action=register   reference=mcp:resource:... connection=scratch
manage_table action=list       reference=mcp:asset:...
manage_table action=unregister registration_id=...
```

`reference` is the string a `search` hit or a `fetch` document carries, passed
verbatim: `mcp:resource:<id>` for material somebody uploaded, `mcp:asset:<id>`
for a saved asset. The kind travels inside the reference, so one action serves
both and there is no argument naming which is which. `table_name` is optional;
the default is a slug of the file's name.

A reference that names no file you may register - one that does not exist, one
that was deleted, one belonging to somebody else - is answered the same way in
every case, so the tool cannot be used to find out which files exist.

**Over REST.**

```
GET    /api/v1/table-connections
GET    /api/v1/resources/{id}/tables
POST   /api/v1/resources/{id}/tables       {"connection": "scratch", "table_name": "vendor_keys"}
DELETE /api/v1/resources/{id}/tables/{registrationID}
GET    /api/v1/portal/assets/{id}/tables
POST   /api/v1/portal/assets/{id}/tables
DELETE /api/v1/portal/assets/{id}/tables/{registrationID}
```

## The lifecycle

Registration is a pointer and a name, not an import. Five things can happen to
one, and the portal's *Query as a table* panel is where four of them are done.

**Register.** Pick a connection and, optionally, a name. The platform reads the
file's header for column names, creates the external table over the directory
the file occupies, and records the registration.

**Register again.** The control stays available after the first registration,
because a file is not limited to one. Registering again does one of two things,
depending on the name:

- A *different* name, or the same name on a different connection, adds a second
  table over the same file. Both stay listed, both work, and neither knows about
  the other. This is how one CSV is queryable from two clusters, or under a name
  that reads better in one team's queries than another's.
- The *same* name on the same connection replaces the registration, provided it
  is a name you registered. This is the repair for staleness below, and it is
  why the panel's warning says to register again rather than to unregister
  first. A name somebody else registered is refused and names who holds it, so
  nothing is silently overwritten; an administrator is unrestricted and does
  replace it.

**Go stale.** See the next section. The table keeps working; it is serving
content that is no longer current.

**Unregister.** Drops the table and forgets the registration. The file is not
touched: dropping a Hive external table removes the catalog entry and nothing
else, and the object stays exactly where it was, byte for byte. Unregistering is
the registrant's call or an administrator's.

**Delete the file.** Deleting the resource or asset drops every table registered
over it, whoever registered them, and forgets the registrations.

## Changing the file: two cases that behave oppositely

Both of these are "I changed the file", and they do not produce the same result,
because a registration points at a directory rather than at a record id.

| What happened | What the table does | What to do |
|---|---|---|
| The object is overwritten at the same key. A vendor drop replacing yesterday's file, a script writing over its own output. | The next query returns the new contents. | Nothing. |
| A new version is written. Every portal asset edit does this, as does every revision of a managed resource: the new content goes to a new directory and the head moves. | The table keeps serving the directory it was registered against. That is correct SQL over the version that was current when it was registered, and it will not change on its own. | Register again, same connection and name. |

The second case is reported as **stale** everywhere a registration is shown: in
the portal panel, on a `search` hit, and in `manage_table action=list`. A stale
table is not broken and is not dropped, so a report built on it keeps running;
it is behind, and the platform will not decide on its own that you wanted it
moved forward.

This is worth knowing before pointing a table at an asset a
[managed script](../scripts/running.md) rewrites on a schedule. Such an asset
gains a version per run, so a table over it is stale from the first refresh
onward and stays that way until somebody registers it again. A file the script
overwrites in place instead has no such problem, and is the better shape when
the table has to stay current by itself.

## Querying what you registered

Every column of a registered table is `VARCHAR`. That is the Hive CSV storage
format's rule, not a platform choice - declaring the table any other way is
refused by Trino itself - so a join to a typed warehouse column needs a cast:

```sql
SELECT s.store_id, s.store_name, u.rebate_pct
FROM warehouse.public.stores s
JOIN scratch.uploads.analyst_vendor_keys u
  ON s.store_id = CAST(u.store_id AS integer)
```

`search` carries the table reference on a hit for a registered file, and
`fetch` carries it on the record, each with a sample statement showing the cast.
Those are the same references `manage_table` takes, so finding a file and
registering it are one turn apart.

## What is refused, and why

**A directory holding a file Trino would read.** Trino reads every non-hidden
object under an external location and parses it as CSV. A stray file beside the
content does not fail the query - it comes back as rows of that file's bytes.
Registration therefore refuses a directory with such a sibling and names it. A
name beginning with `.` or `_` is hidden: Trino skips it, and so does the
check. Thumbnails the portal captures are written under those names, so a CSV
asset that has been rendered in the portal registers with its thumbnails in
place.

Thumbnails captured before those names were adopted are stored as
`thumbnail.png` and `thumbnail_dark.png` - ordinary files that Trino does read,
so an asset still carrying them is refused. An asset carrying one is on the
portal's thumbnail refresh queue for exactly this reason, so opening the portal
in any tab captures it again under the hidden names and removes the objects it
supersedes, after which it registers. Nothing has to be run against the bucket.

**A file whose own name Trino skips.** The same `.`/`_` rule applies to the
file being registered. A table over a hidden object is created, recorded and
queried without any error and returns nothing, so a source under such a name is
refused and the reason is stated. Upload the file under another name.

**A file that is not a CSV.** There is no header row to take column names
from.

**A file that is not yours.** Registering is the authority to change the file,
not the authority to read it. An asset is registrable by its owner or an
administrator; a resource by its uploader, or by whoever may write to the scope
it lives in - a platform administrator, or the administrator of that persona.
That is the same rule that governs updating and deleting each kind, and it
holds on every surface: the portal panel, the REST routes and `manage_table`
resolve the file through one resolver per kind.

The rule is authority to change rather than authority to read because
registering publishes the file's contents into a schema everyone granted the
connection can read, and resource scopes are not carried into Trino. A read
rule would let anyone who can see a persona-scoped file widen its audience.

**A connection your persona is not granted.** Registration meets the same
connection boundary a tool call meets.

**A read-only connection.** The DDL runs through the same read-only check
`trino_execute` runs, so a connection with `read_only: true` refuses it.

**Dropping a table you did not register.** Unregistering is the registrant's
call, or an administrator's: the table lives in a schema everyone with the
connection shares, and the person who put it there is the one who takes it out.
Deleting the file itself drops every table over it, whoever registered them.

**A table name someone else registered.** The scratch schema is shared, so the
name is claimed on registration. Re-registering your own name replaces it;
taking someone else's is refused, naming who holds it. Administrators are
unrestricted.

## What the scratch schema is

The scratch schema is a shared workspace. Everyone granted the connection can
read every table in it, and resource and asset permissions are **not** carried
into Trino - see the [threat model](../security/threat-model.md).

Table names are prefixed with the registering person's persona. That is a
collision-avoidance measure and a legibility one, not a boundary: it keeps an
analyst's `vendors` apart from an administrator's, and it tells a reader of the
schema whose working table they are looking at.

The prefix separates personas rather than people, so two analysts registering
`vendors` do land on the same name, and the prefix is not what decides which of
them gets it. The name is claimed in a unique index on the connection, catalog,
schema and table, and the record of who registered it is what the platform
checks: re-registering a name you hold replaces the table, and a name somebody
else holds is refused rather than overwritten. Administrators are unrestricted,
so an administrator does replace another person's table.

What keeps a registration off the warehouse is the Trino identity the scratch
connection authenticates as. The platform's `read_only` flag is a
statement-prefix denylist evaluated per connection; nothing in the toolkit
restricts a catalog or a schema, and `catalog`/`schema` on a connection are
session defaults rather than bounds. A scratch connection that authenticates as
the same Trino user as the warehouse connection can write to the warehouse. Give
it its own Trino user, with access-control rules allowing DDL only on the
scratch catalog.

## What is recorded

Every registration and unregistration writes an audit event: who, which
connection, the statement that ran, and the table it named. Failed attempts are
recorded too.

## Related

- [Managed Resources](portal-user.md) - uploading the files this registers.
- [Authorization Model](../concepts/authorization.md) - why the Trino identity
  is the boundary.
- [Threat Model](../security/threat-model.md) - what the scratch schema exposes.
