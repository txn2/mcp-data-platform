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
}

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
