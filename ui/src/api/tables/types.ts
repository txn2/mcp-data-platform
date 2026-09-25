// Table registrations make a stored CSV, JSON-lines or Parquet file readable
// as a query-engine table
// (#1327). One shape serves both kinds a file arrives as -- a managed resource
// and a portal asset -- because the registration says the same thing about
// either.

// TableColumn is one column of a registered table and the type it is declared
// as. A CSV's columns are all VARCHAR, the Hive CSV connector's rule, which is
// why a join to a typed warehouse column needs a CAST; a JSON-lines table's are
// typed from its values and a Parquet table's are the file's own (#1833).
export interface TableColumn {
  name: string;
  type: string;
}

// TableFormat is the reader a registered table is declared with.
export type TableFormat = "csv" | "jsonl" | "parquet";

const JSON_LINES_TYPES = ["application/x-ndjson", "application/ndjson", "application/jsonl", "text/x-ndjson"];
const PARQUET_TYPES = ["application/vnd.apache.parquet", "application/x-parquet", "application/parquet"];

// registrableFormat is the format a stored file would be registered in, or
// null for a file no table can be registered over. It decides as the server
// does: by the content type, and by the name where the type is generic or is
// the application/json detection gives a JSON-lines file holding one record.
export function registrableFormat(contentType: string, filename = ""): TableFormat | null {
  const ct = contentType.toLowerCase();
  const named = formatNamed(filename.toLowerCase());
  if (ct.includes("csv")) return "csv";
  if (JSON_LINES_TYPES.some((t) => ct.startsWith(t))) return "jsonl";
  if (PARQUET_TYPES.some((t) => ct.startsWith(t))) return "parquet";
  if (ct === "" || ct.startsWith("application/octet-stream") || ct.startsWith("text/plain")) return named;
  return ct.startsWith("application/json") && named === "jsonl" ? "jsonl" : null;
}

function formatNamed(name: string): TableFormat | null {
  if (name.endsWith(".csv")) return "csv";
  if (name.endsWith(".jsonl") || name.endsWith(".ndjson")) return "jsonl";
  if (name.endsWith(".parquet")) return "parquet";
  return null;
}

// isRegistrableType reports whether a stored file is one a table can be
// registered over: a CSV, JSON lines (#1820), or Parquet (#1833).
export function isRegistrableType(contentType: string, filename = ""): boolean {
  return registrableFormat(contentType, filename) !== null;
}

// formatLabel names a registration's format the way a reader says it.
export function formatLabel(format: TableFormat | undefined): string {
  switch (format) {
    case "jsonl":
      return "JSON lines";
    case "parquet":
      return "Parquet";
    default:
      return "CSV";
  }
}

// columnTypesText says what a table over a file of this format declares its
// columns as.
export function columnTypesText(format: TableFormat | null | undefined): string {
  switch (format) {
    case "jsonl":
      return "Each column is typed from the values the file holds.";
    case "parquet":
      return "Each column has the type the file declares.";
    default:
      return "Every column of a CSV comes back as text.";
  }
}

export interface TableRegistration {
  id: string;
  source_kind: ScratchSourceKind;
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
  // sample_sql shows a query over the table: for a CSV, with the CAST a join
  // to a typed column needs.
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
  // format is the reader the table is declared with (#1820): csv, jsonl for a
  // JSON-lines file, whose values come back exactly -- line breaks and nulls
  // included, which a CSV table cannot carry -- or parquet (#1833).
  format?: TableFormat;
  // all_varchar marks a JSON-lines registration made before its columns were
  // typed (#1833): it keeps every column VARCHAR across a follow, and
  // registering the file again under the same name types them.
  all_varchar?: boolean;
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
  table_registrations: TableRegistration[];
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

// ScratchSourceKind is what a listed table was created for: one of the two
// kinds of file a panel registers over, or an inbound webhook source, whose
// table is created with the source rather than registered from a panel
// (#1870).
export type ScratchSourceKind = TableSourceKind | "webhook";

// --- the cross-source listing (#1472) ---

// ScratchTableSource names the file a registration was built over. The portal
// turns kind and id into the address it opens; the server does not know the
// portal's routes.
export interface ScratchTableSource {
  kind: ScratchSourceKind;
  id: string;
  name?: string;
  // description is the source record's own description. A table name and a
  // file name together still do not say what the data is; this is the
  // sentence that does, and it is absent when the record carries none.
  description?: string;
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
