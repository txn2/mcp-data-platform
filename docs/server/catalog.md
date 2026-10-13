# Platform Catalog

The platform keeps its own data catalog in PostgreSQL. It records the datasets
every connection can see, their columns, the glossary, tags, domains and data
products, and the curation written on each. It is the semantic layer wherever
the platform has a database: table enrichment, `search` over catalog datasets,
glossary lookups and lineage all read from it. No external catalog or ingestion
job is involved.

## What is cataloged

A **dataset** belongs to the connection that sees it. The same table reached
through two connections is two datasets, and a persona sees a dataset only if
its connection rules allow that connection.

| Connection kind | Datasets | Columns |
|-----------------|----------|---------|
| `trino` | every table and view, read from each catalog's `information_schema` | each column with its type and whether it allows nulls |
| `api` | every stored OpenAPI operation | none |
| `graphql` | every query and mutation in the stored schema | each argument, with its type |
| registered and webhook tables | recorded when the registration is made, before the call returns | the columns the registration declares |

The `system` catalog and every catalog's `information_schema` describe the
query engine rather than data, and are never cataloged.

## References

A catalog entity is cited and fetched with an `mcp:` reference:

| Reference | Names |
|-----------|-------|
| `mcp:dataset:(kind,connection,path)` | a dataset. The connection is its instance name; the path is the dotted name the connection uses: `catalog.schema.table` for Trino, the operation id for an API, `query.<field path>` or `mutation.<field path>` for GraphQL. |
| `mcp:column:(kind,connection,path,column)` | one column of a dataset |
| `mcp:glossary_term:<id>`, `mcp:glossary_node:<id>`, `mcp:domain:<id>`, `mcp:data_product:<id>` | a definition, by the id it is stored under. The slug works in place of the id when reading, and renaming a definition changes no reference to it. |
| `mcp:tag:<name>` | a tag |

A part of a dataset or column reference that contains `.`, `,`, `(`, `)` or `"`
is double-quoted the way SQL quotes an identifier, with an inner `"` doubled:
`mcp:dataset:(trino,warehouse,hive.sales."orders.v2")`.

`fetch` dereferences every form. A dataset comes back with its type, the
managed resource holding its schema document when it has one, its columns in
order with each column's curation, every aspect written on it, and the lineage
edges within three hops `upstream` and `downstream` of it.

## Curation

Curation is stored as aspects, each written, versioned and rolled back on its
own:

- on a dataset or a column: description, tags, glossary terms, owners, domain,
  custom properties, structured properties, documentation links
- on a dataset only: curated queries, incidents, context documents
- on a glossary term, glossary node, domain, data product or tag: description,
  owners, custom properties, documentation links

Every write records who made it, when, and the value it replaced. Rolling back
a change restores that value and is recorded as a change of its own. A scan
never touches curation: redeclaring a dataset keeps the curation of every
column that is still declared.

## Scans

Every connection is scanned every 6 hours by default, and once when the
platform starts if it has never been scanned. A scan writes what the connection
sees and marks every dataset it no longer sees as removed. A removed dataset's
curation stays readable, and a later scan that sees it again clears the mark.

A Trino catalog that cannot be read, for example because its connector is
broken, does not fail the connection's scan. The scan writes the catalogs it
could read and reconciles them as usual, records the unreadable catalog as
unread with the reason, and marks nothing in it removed: what it did not read
is not evidence that it is gone. A scan that cannot list the connection at all,
or cannot write to the catalog, fails and marks nothing removed.

Replicas take turns: one advisory lock guards the scan, and the time of the
last scan any replica started decides when the next one is due.

### Admin > Catalog

![Catalog scans](../images/screenshots/light/admin-admin-catalog-light.webp#only-light)![Catalog scans](../images/screenshots/dark/admin-admin-catalog-dark.webp#only-dark)

The **Scans** tab lists each connection's last scan: when it finished, how many
datasets it wrote, how many it marked removed, the problem when it failed, and
each part it could not read ("Not read: legacy", with the reason on hover). The
API reports those parts as `unread`, a list of `{path, error}` that is `[]` when
every part was read.
**Scan now** requests a scan of every connection without waiting for the
interval. The request is recorded, so whichever replica next takes the scan
lock runs it, even when another replica is scanning at that moment.

![Catalog settings](../images/screenshots/light/admin-admin-catalog-settings-light.webp#only-light)![Catalog settings](../images/screenshots/dark/admin-admin-catalog-settings-dark.webp#only-dark)

The **Settings** tab holds the two settings, stored in the database:

- **Scan every**: a duration such as `6h` or `90m`, at least `5m`.
- **Exclusions**: for a connection, the prefixes it leaves out of the catalog,
  separated by commas. For Trino a prefix is a catalog (`hive`), a schema
  (`hive.tmp`) or a table (`hive.tmp.scratch`); for GraphQL, `mutation` leaves
  out every mutation. The next scan leaves the excluded datasets out and marks
  any it cataloged before as removed.

The same settings and scans are served at `/api/v1/admin/catalog/settings` and
`/api/v1/admin/catalog/scans`; see the [Admin API](admin-api.md).

## Search

`search` ranks catalog datasets through the platform's `catalog-datasets`
index, built from this catalog. A dataset declared or curated since the index
last read the catalog is re-read on the index queue's next check
(`knowledge.index_reconcile_interval`, five minutes by default), and the index
re-reads the whole catalog at least every `knowledge.catalog_index.sync_interval`.

## Lineage

Lineage edges are recorded when the platform moves data:

- **SQL**: a statement run through `trino_execute` that writes a table from what
  it reads (`CREATE TABLE ... AS`, `INSERT INTO ... SELECT`, `MERGE INTO`,
  `CREATE VIEW ... AS`) records an edge from each table it read to the table it
  wrote. An unqualified name resolves in the connection's default catalog and
  schema. The statement is read from the audited call, so a deployment with
  `audit.log_parameters: false` records no SQL lineage.
- **Managed scripts**: a run that succeeds records an edge from every table its
  queries read to every file it exported (a managed resource or a portal
  asset).
- **Registrations**: registering a table over a file records an edge from the
  file (`mcp:resource:<id>` or `mcp:asset:<id>`) to the table. A table
  registered over a script's export is therefore reached, walking upstream,
  from the tables the script read.

Each edge records what wrote it: the audited call, the script, or the
registration. The semantic layer's lineage walk reads them.

## The semantic provider

With a database, the platform catalog is the semantic provider. The
`semantic.provider` setting accepts `datahub`, which is read as the platform
catalog until the setting is removed, and `noop`, which turns the semantic
layer off. `semantic.instance`, `semantic.cache` and `semantic.urn_mapping`
have no effect on the catalog: a dataset is keyed by its connection's own
catalog names, so no catalog mapping applies.
