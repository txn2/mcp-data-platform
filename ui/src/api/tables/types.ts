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
