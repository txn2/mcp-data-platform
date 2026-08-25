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

Both connections above reach the same coordinator over HTTPS. On a coordinator
that speaks plain HTTP, each connection has to say so with `ssl: false` and its
port: a connection that never mentions `ssl` is assumed to be HTTPS on 443
unless it is the one named by `default:` or its host is localhost. See
[`ssl`](configuration.md#trino).

A connection with no `scratch:` block cannot hold a registration, and the
surfaces do not offer one on it. Both keys are required: a block naming only
one is ignored with a warning, because a registration built on it would fail at
the DDL.

`read_only: false` on the scratch connection is not decoration either. A
scratch target says *where* a registration writes; it grants nothing. The
statement that creates the table is write SQL, so a `read_only: true`
connection refuses it however its target is configured — and such a connection
is not offered, for the same reason one with no target is not. Naming it
directly anyway — through `manage_table`, or a form built before an
administrator flipped the flag — is refused with **400** and the sentence
*"this connection is read-only, so a table cannot be created on it; ask an
administrator for a connection that accepts writes"*, the same class of answer
as a connection with no scratch target. It is not a 500: the connection is
working exactly as configured, and reporting a configuration fact as a platform
outage told the person neither which connection nor why. What keeps a
registration off the warehouse is not this flag but the Trino identity the
connection authenticates as.

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
manage_table action=register   reference=mcp:resource:... connection=scratch repair=true
manage_table action=list       reference=mcp:asset:...
manage_table action=unregister registration_id=...
```

`reference` is the string a `search` hit or a `fetch` document carries, passed
verbatim: `mcp:resource:<id>` for material somebody uploaded, `mcp:asset:<id>`
for a saved asset. The kind travels inside the reference, so one action serves
both and there is no argument naming which is which. `table_name` is optional;
the default is a slug of the file's name. `repair` is what the second call
above adds: it saves a corrected version of a file that cannot be read as a
table the way it is stored, and registers that. See
[A CSV a query engine cannot read](#a-csv-a-query-engine-cannot-read).

A reference that names no file you may register - one that does not exist, one
that was deleted, one belonging to somebody else - is answered the same way in
every case, so the tool cannot be used to find out which files exist.

**Over REST.**

```
GET    /api/v1/table-connections
GET    /api/v1/resources/{id}/tables
POST   /api/v1/resources/{id}/tables       {"connection": "scratch", "table_name": "vendor_keys", "repair": false}
DELETE /api/v1/resources/{id}/tables/{registrationID}
GET    /api/v1/portal/assets/{id}/tables
POST   /api/v1/portal/assets/{id}/tables
DELETE /api/v1/portal/assets/{id}/tables/{registrationID}
```

## Finding what is registered

The scratch schema is shared: everyone granted the connection sees every table
in it. So the question "what is registered here" is one a reader has to be able
to ask of the platform rather than of one file at a time.

**Scratch Tables**, in the portal's own section list, answers it. One list of
every registration, whichever kind of file each was built over: the qualified
name to write in a `FROM` clause, the connection it lives on, the file behind
it, how many columns it has, who registered it and when, and whether it is
still reading that file's current contents.

![Scratch Tables](../images/screenshots/light/user-scratch-tables-light.webp#only-light)![Scratch Tables](../images/screenshots/dark/user-scratch-tables-dark.webp#only-dark)

What you see is decided by the **connection**, which is the boundary Trino
itself applies: the registrations on connections your persona is granted, and
an administrator sees all of them. It is deliberately wider than the register
form's connection list, which narrows further to connections that can hold a
*new* table. A connection turned read-only after a registration would otherwise
drop that table out of the list for the person who made it, while the engine
went on answering queries against it.

Two states are called out because nothing else can report them. **Behind the
file** is a registration whose source has a newer revision or version than the
table points at, so it serves the one that was current when it was registered.
**Source deleted** is a registration whose file is no longer on the platform;
deleting a file unregisters its tables, so this is the residue of a cleanup
that did not complete.

Opening a row opens the registration at an address of its own: the sample
statement with the cast a join needs, the columns with their types, the file it
came from, and the directory it reads.

![One registered table](../images/screenshots/light/user-scratch-table-detail-light.webp#only-light)![One registered table](../images/screenshots/dark/user-scratch-table-detail-dark.webp#only-dark)

Unregistering is offered here on the tables you may drop, which is the rule the
per-file panel applies: authority over the file, and having registered the
table yourself or being an administrator. Registering stays on the file's own
page, because it needs the file - the platform reads the header row to learn
the columns.

![A table behind its file](../images/screenshots/light/user-scratch-table-stale-light.webp#only-light)![A table behind its file](../images/screenshots/dark/user-scratch-table-stale-dark.webp#only-dark)

The listing is driven by the stored registrations rather than by what can be
registered, so a source of a format the register action does not yet accept
appears here as soon as one exists.

```
GET /api/v1/tables?connection=&kind=&q=&page=&per_page=
GET /api/v1/tables/{registrationID}
```

`kind` is `resource` or `asset`; `q` matches the qualified name. A registration
on a connection you do not reach answers `404`, the same as one that does not
exist.

## The lifecycle

Registration is a pointer and a name, not an import. Five things can happen to
one, and the portal's *Query as a table* panel is where four of them are done.

**Register.** Pick a connection and, optionally, a name. The platform reads the
file's header for column names, creates the external table over the directory
the file occupies, and records the registration.

A registration that fails partway leaves nothing describing something that is
not there, in either direction, and the answer tells the caller to register
again. A table whose row could not be written is dropped: nothing would list
it, and registering the same file again would meet it in Trino and fail on a
name already taken. A table still recorded by the registration it was replacing
is dropped for the opposite reason -- it now reads the new file with the new
columns while the row that survived goes on naming the old ones, and nothing
marks that. And a replacement whose CREATE failed after its DROP had already
run forgets the row of the registration it took over, because the table that
row named is gone. A replacement whose DROP is the statement that failed
changed nothing and is left exactly as it was.

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

## A CSV a query engine cannot read

Trino's Hive CSV reader is line-based: the text input format splits records on
newlines before the quote-aware serde sees them. A line break inside a quoted
cell therefore tears one record into several, the first fragment ends on an
unbalanced quote, and every field after it lands in the wrong column.

This is the shape a spreadsheet export takes whenever one cell holds a
multi-line value - an address, a note, a pasted paragraph. Such a file parses
perfectly under every ordinary CSV reader, so nothing about it looks wrong, and
a table over it is created without error and answers queries with rows. The
rows are fragments.

The same reader splits on the newline and on nothing else, so a file whose
lines end in a bare carriage return - the classic Mac ending some spreadsheet
exports still write - is one record to it: a table over that file has a single
row holding the whole file, and it too is created and queried without error.

Registration therefore reads the whole file before it creates anything - it is
already reading it for the header row - and refuses three things:

- **Lines that end in a carriage return.** The records that end in one are run
  together into a single row, and the refusal says what the lines end in. A
  Windows CRLF ending is not this: every reader on the path folds it to a
  newline, and it costs nothing.
- **A line break inside a cell.** The refusal says how many rows carry one and
  which columns they are in.
- **Bytes that are not UTF-8.** They reach every cell as replacement marks: a
  cell reading `15%` in the source arrives as `15%` followed by mojibake. A NUL
  byte is refused on the same ground even where the rest of the file is valid
  UTF-8, for the reason under *Correcting the file* below.

None of the three is refused silently and none leaves anything behind: no table
is created and no registration is recorded.

The line endings are settled before the rest of the file is read, so a refusal
counts the records the file holds and names the columns it declares, rather
than the single record a reader that splits on the newline alone found in it.
Which carriage returns were line endings is decided from the parse rather than
from the bytes: a lone carriage return is translated, and the translation is
kept only where it recovers records the reader could not see. What counts as
recovered depends on what the file already is.

A file with **no newline anywhere** is one record to every reader on this path,
and no ordinary CSV is written that way - a file that ends its records with
newlines has newlines in it. Nothing about it is ambiguous, so any record the
translation recovers is a record the file holds. This includes a classic Mac
file whose rows do not match its header: it is read as the several records it
is, and then refused by the field-count check below, which is the honest answer
where merging it into one row is not.

A file that **already has newline-delimited records** is the ambiguous one. A
lone carriage return in it is as likely to be a break inside a cell, and
splitting that cell adds a record exactly as a real line ending would. There
the records are counted by the header's width, because a record recovered from
a line ending has the columns the header declares and a fragment torn out of
one cell does not. A carriage return that recovers nothing therefore stays what
it is, a line break inside a cell, and nothing untrue is said about the file's
lines - except in the one shape below, where the two readings cannot be told
apart at all.

One shape stays ambiguous and is decided rather than known: a **single-column**
file with an unquoted carriage return in a value. Every fragment of a
one-column record has the header's width, so field counts cannot separate the
two readings, and the carriage return is taken as a line ending. The correction
says what it did and the version before it is still there. A spreadsheet
writing a multi-line value into a one-column file quotes it, and a quoted one
is read as the cell break it is.

### Correcting the file

The refusal offers the correction, and taking it is one control in the portal
or `repair=true` on the tool call. What it does is a decode and a re-emit:

- the bytes are read as UTF-8, or as windows-1252 when they are not valid
  UTF-8, and a leading byte-order mark is dropped
- a carriage-return line ending becomes a newline, so each record is on its own
  line
- each run of line breaks inside a cell becomes a single space, and the cell is
  trimmed
- the file is written back out as UTF-8 CSV with no byte-order mark

The result is **a new version of the file itself**, written through the version
trail its kind already has: a revision for a managed resource, a version for a
portal asset. Nothing is copied to a second object beside the original, no
mirror is kept, and the file as it was uploaded stays as the version before the
correction - visible in the version panel and restorable from it. The
registration is then built over the new version's directory, so the table is
current from the moment it exists.

The result says what changed, in the portal and in the tool's answer, because
the person's file changed. The same sentence is recorded on the version the
correction wrote, for either kind, so the version panel says why the file
changed without a reader having to find the registration that did it.

A file the correction cannot put right is refused once, and no correction is
offered for it. Two things put a file there. A record whose field count differs
from the header's is one: filling in a short record invents data and dropping a
field from a long one loses some, and neither is a correction the platform can
make on somebody's behalf. A file the reader cannot parse all the way through
is the other: the correction rewrites every record, so it cannot be made over
records it cannot read. Both are settled by the same read of the file that
found the defect, so the refusal names the field counts, or the parse error,
the first time it answers - rather than offering a correction that would then
decline with a different problem. Either way the file has to be fixed where it
was written.

Neither condition refuses a file on its own. A ragged CSV whose lines end in
newlines and whose cells hold no line break registers as it always has, and so
does one the reader gives up on partway: nothing here changes what happens to
them.

Only a single-byte code page is converted. A spreadsheet's *Unicode Text*
export is UTF-16, and every byte of a UTF-16 file is also a valid windows-1252
character, so reading one as a code page produces a character per byte -
mojibake in every cell. A file whose bytes carry a UTF-16 or UTF-32 byte-order
mark, or a NUL byte, is therefore refused outright and no correction is offered
for it: re-export it as UTF-8 CSV and upload that. Nothing is offered that the
platform cannot honestly do.

Nor is every file that is not UTF-8 a windows-1252 one. That code page leaves
five of its 256 byte values with no character at all - `0x81`, `0x8D`, `0x8F`,
`0x90` and `0x9D` - and a decoder asked to read one of them puts a replacement
mark in its place and reports no error. A file holding one is in some other
encoding, or is not text; converting it would write those marks into the
person's new version and call it a conversion from windows-1252. So the code
page is the answer only where every byte is one it defines, and a file carrying
any of the five is refused with the same instruction: re-export it as UTF-8 CSV
and upload that. The bytes on either side of them - `0x80`, `0x8E`, `0x9E` and
`0x9F` - all have characters and still convert.

The NUL byte is looked for before the UTF-8 check rather than after it, because
a NUL is itself valid UTF-8. The same *Unicode Text* export written without a
byte-order mark, over content that is plain ASCII, is a valid UTF-8 file with a
NUL beside every character; a UTF-8 check alone passes it, and the table it
would register carries those NULs in its column names and in every cell.

Such a file is then reported on its encoding alone. The refusal says nothing
about its line endings, how many of its rows are torn, or what its columns are
called, because each of those readings would be taken from bytes read in the
wrong encoding: a Windows line ending in a UTF-16 file puts a carriage return
beside a NUL, which a reader of single-byte text takes for a line break inside
a cell, and the column it would name is the run of NUL-laden bytes around it
rather than a name the file holds.

Where the NUL is what identified the file, the refusal names the NUL rather
than an encoding, since nothing else about those bytes says what they are.

A correction is written before the last of the checks have run, so a refusal -
or a coordinator that would not run the statement - can arrive after the file
has already changed. The answer says so in that case, and the audit event
records the correction whether or not the registration it was for succeeded.
The file stays corrected; registering it again creates the table.

A file that is already line-safe and valid UTF-8 with no NUL in it registers
exactly as it always did. No version is written, nothing is rewritten, and the
result says nothing extra - including when the correction was asked for and
turned out to be unnecessary.

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

**A CSV a line-based reader cannot read.** Lines that end in a carriage return
rather than a newline, a line break inside a cell, or bytes that are not
readable as UTF-8 text - which a NUL byte makes them, whether or not the rest
of the file is valid UTF-8.
See
[A CSV a query engine cannot read](#a-csv-a-query-engine-cannot-read).

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
recorded too. A registration that corrected the file first records what the
correction changed on the same event, because it rewrote somebody's file on
their behalf - and it records it even when the registration went on to be
refused, since the file changed either way.

## Related

- [Managed Resources](portal-user.md) - uploading the files this registers.
- [Authorization Model](../concepts/authorization.md) - why the Trino identity
  is the boundary.
- [Threat Model](../security/threat-model.md) - what the scratch schema exposes.
