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
| `mcp:schema_document:(kind,connection,filename)` | an API or GraphQL connection's schema document |
| `mcp:glossary_term:<id>`, `mcp:glossary_node:<id>`, `mcp:domain:<id>`, `mcp:data_product:<id>` | a definition, by the id it is stored under. The slug works in place of the id when reading, and renaming a definition changes no reference to it. |
| `mcp:tag:<name>` | a tag |

A part of a dataset or column reference that contains `.`, `,`, `(`, `)` or `"`
is double-quoted the way SQL quotes an identifier, with an inner `"` doubled:
`mcp:dataset:(trino,warehouse,hive.sales."orders.v2")`.

`fetch` dereferences every form. A dataset comes back with its type, the
schema document it was read from when it has one, its columns in order with
each column's curation, every aspect written on it, and the lineage
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

Curation is written with `apply_knowledge` (its default sink, `catalog`): the
changes of one apply are written in one transaction, recorded as a changeset,
and a rollback restores every aspect the apply wrote, refused when one of them
has been written again since. An apply that finds an aspect it read written by
someone else before its own write lands reads and plans again, so two applies
to one entity at once both land. See [Governance](../knowledge/governance.md).
A promoted call record becomes a curated query on every dataset its statement
read.

A knowledge page cites a catalog entity by its `mcp:` reference, in its
references or its body, and the citation is checked against the catalog when
it is written.

## Schema documents

An API connection's OpenAPI spec and a GraphQL connection's schema (as SDL) are
kept in the catalog, one per connection, addressed as
`mcp:schema_document:(kind,connection,filename)`, for example
`mcp:schema_document:(api,billing,"openapi.json")`. Each operation's dataset
names the document it was read from: `fetch` on the dataset lists it as a
reference, and its `schema_document` field holds it. A scan records a new
version only when the document's content or media type changed, so an
unchanged spec stays at its version.

A document belongs to its connection. `fetch` returns it only to a caller whose
persona may reach that connection, the rule its datasets are read under, and
answers not found to anyone else. Its text is the body, up to the 1 MiB every
fetched file is held to; a longer one is cut at a character boundary with a
note saying how much was shown. An administrator lists every document on
**Admin > Catalog > Documents**, or with `GET /api/v1/admin/catalog/documents`,
and downloads one from there. A knowledge page may cite one. A page's citation of a schema document, a dataset or a column is listed, by `fetch` and on the portal, only to a reader whose persona may reach its connection; another reader's `fetch` counts it in `references_withheld`.

![Schema documents](../images/screenshots/light/admin-admin-catalog-documents-light.webp#only-light)![Schema documents](../images/screenshots/dark/admin-admin-catalog-documents-dark.webp#only-dark)

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

The **Import** tab carries a DataHub into the catalog; see
[Moving from DataHub](#moving-from-datahub).

The same settings and scans are served at `/api/v1/admin/catalog/settings` and
`/api/v1/admin/catalog/scans`, the schema documents at
`/api/v1/admin/catalog/documents` and the imports at
`/api/v1/admin/catalog/imports`; see the [Admin API](admin-api.md).

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

### Inherited column curation

A column with no description or no glossary terms of its own shows the
same-named column's from the nearest upstream dataset that has them, up to two
hops up its lineage (#2130). Columns match by name, ignoring case; the catalog's
lineage is between datasets, not columns. A column keeps whatever curation it
has and borrows only what it lacks, and tags are never borrowed, since a tag
such as `pii` says something about the column itself. An upstream dataset on a
connection the caller's persona does not reach lends nothing.

Inherited curation reaches an agent in the column context of a Trino result,
marked with where it came from:

```json
"column_context": {
  "email": {
    "description": "Where the customer is contacted.",
    "inherited_from": {
      "source_dataset": "mcp:dataset:(trino,warehouse,pg.crm.customers)",
      "source_column": "email", "hops": 1, "match_method": "name_exact"
    }
  }
},
"inheritance_sources": ["mcp:dataset:(trino,warehouse,pg.crm.customers)"]
```

It is never written to the downstream column, so curating the upstream column
changes what every downstream column shows. `enrichment.lineage_inheritance:
false` turns it off.

## The portal catalog

The portal's **Knowledge > Catalog** section is where people read and curate
the catalog. It opens on **Tables** and has four more tabs: **Glossary**,
**Tags**, **Domains** and **Data products**. Each tab is addressed in the URL
hash (`/portal/knowledge/catalog#glossary`), and an open entity in the `ref`
query parameter, so a catalog reference anywhere in the portal links straight
to it: `/portal/knowledge/catalog?ref=mcp%3Atag%3Apii#tags`.

![Catalog tables](../images/screenshots/light/user-knowledge-catalog-light.webp#only-light)![Catalog tables](../images/screenshots/dark/user-knowledge-catalog-dark.webp#only-dark)

**Tables** finds datasets across every connection the reader's persona
reaches, by text in the name, path or description, optionally on one
connection. A dataset's page shows and, for an editor, edits:

- its description, tags, glossary terms, owners, domain and properties;
- its columns in table order, each opened with **Edit** to change its own
  description, tags and glossary terms;
- its context documents: markdown notes with a title and a category;
- its lineage, upstream and downstream within three hops, each dataset a link
  that opens it in place;
- the schema document it was read from, for an API or GraphQL operation;
- the knowledge pages that cite it;
- its history: every change with who wrote it, when, what it now holds and what
  it replaced, each with **Roll back**. A column's history is on its editor.
  A change the same aspect was written over since is not rolled back, since
  that would discard the newer write too; the refusal names that write.

![A dataset's page](../images/screenshots/light/user-knowledge-catalog-dataset-light.webp#only-light)![A dataset's page](../images/screenshots/dark/user-knowledge-catalog-dataset-dark.webp#only-dark)

![A column opened for editing](../images/screenshots/light/user-knowledge-catalog-column-edit-light.webp#only-light)![A column opened for editing](../images/screenshots/dark/user-knowledge-catalog-column-edit-dark.webp#only-dark)

A tag is picked from the tags the catalog defines, or typed as a new name. A
glossary term and a domain are picked by name from the catalog's definitions.

**Glossary** lists the top of the glossary, or every term and node matching a
filter. A node's page lists what is filed under it and adds an entry there; a
term's page lists the datasets and columns that carry it. **Tags**, **Domains**
and **Data products** list their definitions the same way. Every definition's
page edits its description, shows its history, lists the knowledge pages that
cite it, and deletes it. A definition still in use is not deleted: a node with
entries under it, and a term, tag or domain a dataset or column carries, is
refused with what still uses it.

![A glossary term's page](../images/screenshots/light/user-knowledge-catalog-glossary-term-light.webp#only-light)![A glossary term's page](../images/screenshots/dark/user-knowledge-catalog-glossary-term-dark.webp#only-dark)

Reading the catalog takes the `fetch` tool on the reader's persona, and editing
it the `apply_knowledge` tool, the two tools that read and curate it over MCP.
The Catalog tab is shown only to a reader who may read it. A dataset, its
columns and its schema document belong to a connection, and are shown and
written only for a reader whose persona reaches that connection; a
definition's list of carriers counts the ones on other connections instead of
naming them. An administrator reaches everything. Every write is audited as an
`apply_knowledge` call with source `portal`. `apply_knowledge` holds a curation write to the same boundary: an apply or a
rollback on a dataset or column of a connection the caller's persona does not
reach is refused, and so is a `bulk_untag` of a tag such a dataset carries.
An edit made in the portal carries the value it was made from, so an edit
from a page another writer has since changed is refused with a request to
reload, rather than replacing that writer's change.

### Portal catalog API

The portal reads and writes through `/api/v1/portal/catalog`, under the same
rules:

| Route | Does |
|-------|------|
| `GET /connections` | the connections the caller reaches, and whether the caller may edit |
| `GET /datasets` | search datasets: `q`, `connection_kind`, `connection`, `tag` (repeatable), `limit` (default 50, at most 200), `offset` |
| `GET /browse` | one level of a connection's datasets: `connection_kind`, `connection`, `path` |
| `GET /dataset?ref=` | a dataset with its columns, curation, lineage within three hops, and the names of the terms and domain it refers to |
| `GET /schema-document?ref=` | a schema document and its text |
| `GET /history?ref=` | the changes to a dataset, column or definition, newest first; `aspect` narrows to one |
| `PUT /aspect` | write one aspect: `{"reference", "aspect", "value", "expect"}`; a `null` value clears it; `expect`, the value the edit was made from, refuses with `409` a write made since, which the portal always sends |
| `POST /rollback` | undo a change: `{"reference", "aspect", "change_id"}`; 409 when the aspect was written over since |
| `GET /definitions` | list definitions: `kind` (repeatable), `parent`, `roots`, `q`, `limit`, `offset` |
| `POST /definitions` | create a definition: `{"kind", "name", "parent", "description"}`; `409` naming the existing one when a definition of the kind already has the name's slug |
| `GET /definition?ref=` | a definition, the entries under a glossary node, and up to 100 carriers; `more_uses` when more carry it |
| `DELETE /definition?ref=` | delete a definition; `409` while something uses it |

See the [Admin API](admin-api.md) reference for each route's schema.

## Moving from DataHub

The platform no longer reads or writes DataHub, apart from importing it once.
A deployment upgrading with DataHub still in its configuration starts as
before: the platform reads the DataHub `toolkits.datahub` names (the instance
`semantic.instance` chose, else the kind's `default`, else the first by name;
its `url` or `endpoint`, and its `token`) and imports it into the platform
catalog after its first catalog scan, so the curation lands on the datasets
the scan cataloged. Until an import against that address finishes without
error, the platform tries again after each scan; after one has, a restart
imports nothing. One import runs at a time across replicas.

Nothing else of DataHub runs: no `datahub_*` tools, no DataHub enrichment, and
`semantic.provider: datahub` is read as the platform catalog.
These settings are accepted for this release, strict parsing included, and
logged once at startup as `deprecated_key`: `semantic.provider: datahub`,
`toolkits.datahub`, `semantic.instance`, `semantic.urn_mapping`,
`semantic.lineage`, `query.urn_mapping`, the `storage:` block,
`enrichment.datahub_query_enrichment`, `enrichment.datahub_storage_enrichment`,
`enrichment.s3_semantic_enrichment`, `enrichment.search_schema_preview`,
`enrichment.schema_preview_max_columns` and
`knowledge.apply.datahub_connection`. Remove them once the import has finished:
the next release refuses them.

Lineage inheritance carries over without its settings (#2130). Through v1.145,
`semantic.lineage` let a column with no curation of its own show its upstream
column's along DataHub lineage. The platform catalog does the same along its own
lineage, which the import carries over, and it is on by default; the
`semantic.lineage` settings themselves are not read. See
[Inherited column curation](#inherited-column-curation).

### What the import carries over

![Catalog import](../images/screenshots/light/admin-admin-catalog-import-light.webp#only-light)![Catalog import](../images/screenshots/dark/admin-admin-catalog-import-dark.webp#only-dark)

**Admin > Catalog > Import** shows the configured DataHub, imports it again,
or imports another DataHub given its address and an access token (used for that
run and not kept), and lists each run with its report
(`GET`/`POST /api/v1/admin/catalog/imports`). The import reads:

- every dataset DataHub holds, including the records it has marked removed. A
  live record's curation is taken; an aspect it lacks is taken from a removed
  record of the same path, so curation that exists only on a removed record is
  kept.
- the description (DataHub's edited one over its ingested one), tags, glossary
  terms, owners, domain, documentation links, custom and structured
  properties, curated queries, incidents and context documents of each
  dataset, and the description, tags and glossary terms of each top-level
  column.
- every glossary node, glossary term, tag, domain and data product, with its
  description, owners and documentation links. A term keeps the node it sits
  under.
- lineage between the datasets the catalog holds, recorded as the import's.

A dataset on a Trino platform is matched by its name, with a leading platform
instance dropped and `semantic.urn_mapping.catalog_mapping` read backwards;
`semantic.urn_mapping.platform` names the platform the deployment's datasets
were recorded under when it is not `trino`. Tableau entities, and datasets on
any other platform, are not imported.

An aspect something other than the import has written in the platform catalog
is kept, and the report says so. Importing again writes only what changed in
DataHub since, and keeps each definition under the key it was first stored
under.

Then the import rewrites the DataHub URNs platform tables hold into catalog
references: memories and insights (their entity references, column
references and text), changesets, call records, knowledge pages (their body,
summary and citations, which become catalog citations) and the rows of the
dataset search index keyed by a DataHub URN, which the index reads again from
the catalog. A URN the import has no catalog entity for (a user, a query
entity, a dataset no connection catalogs) is left as it was and listed in the
report with the table and row it was found in.

The report counts the definitions and datasets imported, the datasets that took
curation from a removed record, the aspects written, left unchanged and kept,
the lineage edges, and the references rewritten, and lists what was not
carried over and why.

### After the import

A catalog entity is referenced only by its `mcp:` reference. An `apply_knowledge`
target, a knowledge page citation and a semantic lookup given a `urn:li:` URN
are refused with the reference forms they take. A changeset applied to DataHub
before the catalog replaced it cannot be rolled back, since its writes are not
in the catalog's history; a new apply restores the state wanted.

## The semantic provider

With a database, the platform catalog is the semantic provider; no `semantic:`
block is needed. `semantic.provider: noop` turns the semantic layer off. Lookups read the
catalog directly, so `semantic.cache` has no effect on them. A dataset is keyed by its connection's own
catalog names, so no name mapping applies.
