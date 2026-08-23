import type {
  TableConnection,
  TableRegistration,
} from "@/api/tables/types";

// Table registrations (#1327). The fixtures cover the states the panel renders
// differently: a current registration, one whose file has moved on since, a
// file with none at all, and a CSV a query engine cannot read the way it is
// stored (#1441).

// scratchConnection is the one connection the fixtures register onto, named
// separately so the register helper can fall back to it without an
// index-into-a-list that the type system has to be told is populated.
const scratchConnection: TableConnection = {
  name: "acme-scratch",
  description: "Writable scratch catalog beside the warehouse",
  catalog: "scratch",
  schema: "uploads",
};

export const mockTableConnections: TableConnection[] = [scratchConnection];

export const mockTableRegistrations: Record<string, TableRegistration[]> = {
  "ast-008": [
    {
      id: "reg_2f1c8a",
      source_kind: "asset",
      source_id: "ast-008",
      connection: "acme-scratch",
      catalog: "scratch",
      schema: "uploads",
      table: "analyst_regional_sales_summary",
      location: "s3://portal-assets/assets/",
      columns: [
        { name: "region", type: "VARCHAR" },
        { name: "quarter", type: "VARCHAR" },
        { name: "revenue", type: "VARCHAR" },
        { name: "units", type: "VARCHAR" },
      ],
      registered_by: "alice@example.com",
      registered_at: "2026-08-20T14:12:00Z",
      query_table: "scratch.uploads.analyst_regional_sales_summary",
      sample_sql:
        'SELECT * FROM scratch.uploads.analyst_regional_sales_summary\n-- every column is VARCHAR, so a join to a typed column casts:\n-- JOIN scratch.uploads.analyst_regional_sales_summary t ON w.id = CAST(t."region" AS BIGINT)',
      stale: false,
    },
  ],
  // A registration left behind by an earlier revision: the file has a newer
  // version than the table points at, which is the one state a reader cannot
  // discover from the rows themselves.
  "res-015": [
    {
      id: "reg_7b3d90",
      source_kind: "resource",
      source_id: "res-015",
      connection: "acme-scratch",
      catalog: "scratch",
      schema: "uploads",
      table: "analyst_glossary",
      location: "s3://managed-resources/resources/global/reference/v/rev-1/",
      columns: [
        { name: "term", type: "VARCHAR" },
        { name: "definition", type: "VARCHAR" },
        { name: "owner", type: "VARCHAR" },
      ],
      registered_by: "marcus.johnson@example.com",
      registered_at: "2026-08-18T09:30:00Z",
      query_table: "scratch.uploads.analyst_glossary",
      stale: true,
    },
  ],
};

// tornCSVSourceID is the fixture standing for a spreadsheet export whose cells
// carry line breaks -- a multi-line store address in one cell. A query engine
// splits records on newlines before it sees the quotes, so each of those rows
// would be torn into fragments with every later field in the wrong column, and
// nothing about the resulting table would say so. Registering it is refused
// until a corrected version of the file is saved (#1441).
export const tornCSVSourceID = "res-011";

// tornCSVProblem is the refusal that file meets. The detail is the sentence a
// person reads; the type is what the form keys its offer of the correction on.
export const tornCSVProblem = {
  type: "urn:mcp-data-platform:problem:csv-needs-repair",
  title: "Conflict",
  status: 409,
  detail:
    "94 rows in this file have a line break inside a cell (in address), and a table reads a line " +
    "break as the end of the row, so each of those rows would be torn into fragments. Register it " +
    "again asking for the file to be corrected, and a corrected version is saved and registered; " +
    "the file as it was uploaded stays as the version before it.",
};

// mockRegisterTable adds a registration the way the backend would: the
// persona-prefixed name, the columns of the file's header, and the location of
// the directory the file already sits in. A file that cannot be read as a table
// the way it is stored comes back as a refusal instead, unless the correction
// was asked for.
export async function mockRegisterTable(
  kind: "resource" | "asset",
  sourceID: string,
  request: Request,
): Promise<TableRegistration | typeof tornCSVProblem> {
  const body = (await request.json()) as {
    connection: string;
    table_name?: string;
    repair?: boolean;
  };
  if (sourceID === tornCSVSourceID && !body.repair) {
    return tornCSVProblem;
  }
  const conn = mockTableConnections.find((c) => c.name === body.connection) ?? scratchConnection;
  const slug = (body.table_name || "uploaded_file")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
  const table = slug.startsWith("analyst_") ? slug : `analyst_${slug}`;

  const reg: TableRegistration = {
    id: `reg_${Math.random().toString(16).slice(2, 8)}`,
    source_kind: kind,
    source_id: sourceID,
    connection: conn.name,
    catalog: conn.catalog,
    schema: conn.schema,
    table,
    location: `s3://portal-assets/${kind}s/${sourceID}/`,
    columns: [
      { name: "region", type: "VARCHAR" },
      { name: "quarter", type: "VARCHAR" },
      { name: "revenue", type: "VARCHAR" },
    ],
    registered_by: "alice@example.com",
    registered_at: new Date("2026-08-22T10:00:00Z").toISOString(),
    query_table: `${conn.catalog}.${conn.schema}.${table}`,
    stale: false,
    repaired:
      sourceID === tornCSVSourceID
        ? "Saved version 2 of this file, which put 94 rows back onto one line. The file as it was " +
          "uploaded is still there as the version before it."
        : undefined,
  };
  mockTableRegistrations[sourceID] = [reg, ...(mockTableRegistrations[sourceID] ?? [])];
  return reg;
}

// mockDropTable removes one registration, leaving the file itself alone -- the
// same thing a real DROP of an external table does.
export function mockDropTable(sourceID: string, registrationID: string): void {
  const rows = mockTableRegistrations[sourceID];
  if (!rows) {
    return;
  }
  mockTableRegistrations[sourceID] = rows.filter((r) => r.id !== registrationID);
}
