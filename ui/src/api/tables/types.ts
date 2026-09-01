// Table registrations make a stored CSV readable as a query-engine table
// (#1327). One shape serves both kinds a file arrives as -- a managed resource
// and a portal asset -- because the registration says the same thing about
// either.

// TableColumn is one column of a registered table. Every column is VARCHAR:
// that is the Hive CSV connector's rule, not a choice, which is why a join to
// a typed warehouse column needs a CAST.
export interface TableColumn {
  name: string;
  type: string;
}

export interface TableRegistration {
  id: string;
  source_kind: "resource" | "asset";
  source_id: string;
  connection: string;
  catalog: string;
  schema: string;
  table: string;
  location: string;
  columns: TableColumn[];
  registered_by: string;
  registered_at: string;
  // query_table is the name to write in a FROM clause.
  query_table: string;
  // sample_sql shows the CAST a join needs.
  sample_sql?: string;
  // stale means the file has a newer version than the one the table points
  // at, so the rows are the version that was current when it was registered.
  stale: boolean;
  // follow means the table is moved onto each new revision or version of the
  // file as it is written (#1536), which is what a registration gets unless
  // it was pinned. follow_error is why the last follow did not move it, and
  // is absent while the table is where the file is.
  follow: boolean;
  follow_error?: string;
  // repair means the table corrects its file: a new version carrying a defect
  // a query engine cannot read past, of the kind the platform can correct, is
  // saved corrected as the file's next version and the table is moved onto
  // that version (#1577). It is the choice made when the table was registered,
  // and it only does anything for a table that follows its file.
  repair: boolean;
  // repaired says what a correction of the file changed before it could be
  // registered (#1441). It is set only on the registration that made the
  // correction: it describes what just happened, not a property of the record.
  repaired?: string;
}

// CSV_NEEDS_REPAIR is the problem type a registration is refused with when the
// file cannot be read as a table the way it is stored -- lines that end in a
// carriage return rather than a newline, a line break inside a cell, or bytes
// that are not UTF-8 -- but could be if a corrected version of it were saved
// first. The detail carries the sentence a person reads; this is
// the half the form matches on to offer that correction.
export const CSV_NEEDS_REPAIR = "urn:mcp-data-platform:problem:csv-needs-repair";

export interface TableRegistrationList {
  registrations: TableRegistration[];
}

// TableConnection is one connection a table can be registered onto: granted to
// the caller's persona and carrying a scratch catalog and schema.
export interface TableConnection {
  name: string;
  description?: string;
  catalog: string;
  schema: string;
}

export interface TableConnectionList {
  connections: TableConnection[];
}

// TableSourceKind selects which routes a panel talks to.
export type TableSourceKind = "resource" | "asset";

// --- the cross-source listing (#1472) ---

// ScratchTableSource names the file a registration was built over. The portal
// turns kind and id into the address it opens; the server does not know the
// portal's routes.
export interface ScratchTableSource {
  kind: TableSourceKind;
  id: string;
  name?: string;
  // missing says the source record is gone. Deleting a file unregisters its
  // tables, so this is the residue of a cleanup that did not complete.
  missing: boolean;
}

// ScratchTable is one registration as the Scratch Tables listing renders it:
// the registration, the file it came from, and whether this reader is offered
// the action that drops it.
export interface ScratchTable extends TableRegistration {
  source: ScratchTableSource;
  can_unregister: boolean;
}

export interface ScratchTableList {
  data: ScratchTable[];
  total: number;
  page: number;
  per_page: number;
}

// ScratchTableQuery is the listing's facets: which connection, which kind of
// file, and free text over the qualified name.
export interface ScratchTableQuery {
  page?: number;
  perPage?: number;
  connection?: string;
  kind?: TableSourceKind | "";
  q?: string;
}
