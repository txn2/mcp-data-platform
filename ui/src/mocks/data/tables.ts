import type {
  ScratchTable,
  TableColumn,
  TableConnection,
  TableRegistration,
} from "@/api/tables/types";
import { mockAssets } from "./assets";
import { mockContent } from "./content";
import {
  defectReason,
  fixtureColumns,
  inspectFixture,
  normalizeFixture,
  repairSummary,
} from "./csvfixture";
import { isCorrected, mockResourceContent, mockResources, recordCorrection } from "./resources";

// sourceColumns is the columns a table registered over a stored file declares:
// the header of the bytes that file serves. Every registration below reads its
// columns from here rather than listing them, because a list beside the file
// it describes is a list that can disagree with it -- which is what #1617 was
// filed over, with the same three names reported for every file registered
// through the form.
function sourceColumns(kind: "resource" | "asset", sourceID: string): TableColumn[] {
  const body = kind === "asset" ? mockContent[sourceID] : mockResourceContent[sourceID];
  return fixtureColumns(body ?? "");
}

// sourceFilename is what the file is called, which is what a table registered
// over it is named after when the person leaves the name field alone.
function sourceFilename(kind: "resource" | "asset", sourceID: string): string {
  if (kind === "asset") {
    const asset = mockAssets.find((a) => a.id === sourceID);
    return asset ? (asset.s3_key.split("/").pop() ?? asset.name) : "";
  }
  return mockResources.resources.find((r) => r.id === sourceID)?.filename ?? "";
}

// sourceLocation is the external location a table registered over a file
// addresses: the directory the file already sits in, in the bucket that file's
// own URI names. The register helper addressed every source in the asset
// bucket, so a resource's table claimed to read bytes from a bucket the
// resource is not in (#1617).
function sourceLocation(kind: "resource" | "asset", sourceID: string): string {
  const key =
    kind === "asset"
      ? mockAssets.find((a) => a.id === sourceID)?.s3_key
      : mockResources.resources.find((r) => r.id === sourceID)?.s3_key;
  const bucket = kind === "asset" ? "portal-assets" : "acme-platform";
  const dir = (key ?? "").split("/").slice(0, -1).join("/");
  return `s3://${bucket}/${dir}/`;
}

// slugify renders a filename or a typed suggestion as a table name, as
// tableregister.SlugifyTableName does: the extension dropped, then lowercase
// letters and digits with every other run collapsed to one underscore.
function slugify(raw: string): string {
  const stem = raw.replace(/\.[^.]+$/, "");
  return stem
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
}

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
      columns: sourceColumns("asset", "ast-008"),
      registered_by: "alice@example.com",
      registered_at: "2026-08-20T14:12:00Z",
      query_table: "scratch.uploads.analyst_regional_sales_summary",
      sample_sql:
        'SELECT * FROM scratch.uploads.analyst_regional_sales_summary\n-- every column is VARCHAR, so a join to a typed column casts:\n-- JOIN scratch.uploads.analyst_regional_sales_summary t ON w.id = CAST(t."region" AS BIGINT)',
      stale: false,
      follow: true,
      // Registered with the correction on, so this table corrects its file:
      // the state the panel renders a second badge for (#1577).
      repair: true,
    },
  ],
  // A pinned registration left behind by a later revision: the file has a
  // newer version than the table points at, which is the one state a reader
  // cannot discover from the rows themselves.
  "res-015": [
    {
      id: "reg_7b3d90",
      source_kind: "resource",
      source_id: "res-015",
      connection: "acme-scratch",
      catalog: "scratch",
      schema: "uploads",
      table: "analyst_glossary",
      location: "s3://acme-platform/resources/global/reference/v/rev-1/",
      columns: sourceColumns("resource", "res-015"),
      registered_by: "marcus.johnson@example.com",
      registered_at: "2026-08-18T09:30:00Z",
      query_table: "scratch.uploads.analyst_glossary",
      sample_sql: "SELECT * FROM scratch.uploads.analyst_glossary",
      stale: true,
      follow: false,
      repair: false,
    },
  ],
  // A following registration whose last follow did not move it (#1536): the
  // coordinator refused the statement, so the table is behind the file with
  // the reason on it until it is registered again.
  //
  // It sits on a CSV resource, which is the only kind of file a table can be
  // registered over. It was on the HTML onboarding guide until #1617: the
  // listing showed a registered table whose source, followed to its page, was
  // a document that carries no such panel and never could.
  "res-008": [
    {
      id: "reg_e19c42",
      source_kind: "resource",
      source_id: "res-008",
      connection: "acme-scratch",
      catalog: "scratch",
      schema: "uploads",
      table: "analyst_seasonal_factors",
      location: "s3://acme-platform/resources/persona/inventory-analyst/reference/v/rev-1/",
      columns: sourceColumns("resource", "res-008"),
      registered_by: "marcus.johnson@example.com",
      registered_at: "2026-08-19T16:40:00Z",
      query_table: "scratch.uploads.analyst_seasonal_factors",
      sample_sql: "SELECT * FROM scratch.uploads.analyst_seasonal_factors",
      stale: true,
      follow: true,
      repair: false,
      follow_error:
        "registering the table: the coordinator refused the statement (Access Denied: Cannot create table scratch.uploads.analyst_seasonal_factors)",
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

// The uploaded bytes of that file, read once at load: the refusal and the
// summary of the correction are both about the file as its owner uploaded it,
// which is what a correction replaces. Reading them here rather than writing
// them out is what keeps the panel and the preview beside it describing one
// file (#1617) -- the refusal named 94 rows in an `address` column of a
// ten-row file that had no such column.
const tornCSVUploaded = mockResourceContent[tornCSVSourceID] ?? "";

// tornCSVProblem is the refusal that file meets. The detail is the sentence a
// person reads; the type is what the form keys its offer of the correction on.
export const tornCSVProblem = {
  type: "urn:mcp-data-platform:problem:csv-needs-repair",
  title: "Conflict",
  status: 409,
  detail:
    `${defectReason(inspectFixture(tornCSVUploaded))} Register it ` +
    "again asking for the file to be corrected, and a corrected version is saved and registered; " +
    "the file as it was uploaded stays as the version before it.",
};

// tornCSVRepairSummary is what the correction did, in the terms the backend's
// repairSummary renders. One constant because the registration's answer and the
// version the correction wrote say the same thing (#1450).
export const tornCSVRepairSummary = repairSummary(normalizeFixture(tornCSVUploaded).rowsRepaired);

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
    follow?: boolean;
  };
  // A file already corrected in this session is readable as a table, so the
  // second registration of it is an ordinary one -- the same thing the backend
  // does, where the refusal comes from inspecting the bytes the head points at
  // rather than from the file's identity.
  const needsRepair = sourceID === tornCSVSourceID && !isCorrected(sourceID);
  if (needsRepair && !body.repair) {
    return tornCSVProblem;
  }
  const conn = mockTableConnections.find((c) => c.name === body.connection) ?? scratchConnection;
  // An untouched name field takes the file's own name, which is what the form
  // shows as the placeholder and what the backend derives. It used to fall
  // back to a fixed "uploaded_file", so a capture of the form suggesting
  // `store_list` was followed by a capture of a table called
  // `analyst_uploaded_file` (#1617).
  const slug = slugify(body.table_name || sourceFilename(kind, sourceID));
  const table = slug.startsWith("analyst_") ? slug : `analyst_${slug}`;

  const registeredBy = "alice@example.com";
  const repaired = needsRepair
    ? `Saved version 2 of this file, which ${tornCSVRepairSummary}. The file as it was ` +
      "uploaded is still there as the version before it."
    : undefined;
  // The correction is a version of the file, not a property of the
  // registration: it outlives this answer and is what the version panel shows.
  // It happens BEFORE the registration is built, because the registration is
  // built over the corrected version's directory -- reading the location first
  // would address the version the correction replaced.
  if (repaired) {
    recordCorrection(sourceID, tornCSVRepairSummary, registeredBy);
  }

  const reg: TableRegistration = {
    id: `reg_${Math.random().toString(16).slice(2, 8)}`,
    source_kind: kind,
    source_id: sourceID,
    connection: conn.name,
    catalog: conn.catalog,
    schema: conn.schema,
    table,
    location: sourceLocation(kind, sourceID),
    columns: sourceColumns(kind, sourceID),
    registered_by: registeredBy,
    registered_at: new Date("2026-08-22T10:00:00Z").toISOString(),
    query_table: `${conn.catalog}.${conn.schema}.${table}`,
    stale: false,
    // Following is the default; the form sends follow only to turn it off.
    follow: body.follow !== false,
    // The correction is a standing choice, kept on the registration: the
    // second submission of the form is what asks for it (#1577).
    repair: body.repair === true,
    repaired,
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

// --- the cross-source listing (#1472) ---

// scratchTableSources names the file behind each registration, the way the
// listing route does by reading the two source stores. A source id with no
// entry here stands for a record that is gone, which the listing marks so the
// reader is not sent to a page that answers "no such file".
//
// Each name is the one its own page carries. A listing that renames the file it
// links to sends the reader looking for something that is not there (#1617).
const scratchTableSources: Record<string, { name: string; canModify: boolean }> = {
  "ast-008": { name: "Regional Sales Summary", canModify: true },
  "res-015": { name: "Business Glossary Export", canModify: true },
  // Somebody else's upload: visible because the reader reaches the connection,
  // and not theirs to drop. Its table follows the file and its last follow
  // failed, which is the listing's fourth state.
  "res-008": { name: "Seasonal Factors", canModify: false },
};

// orphanedRegistration is a table whose file is no longer on the platform.
// Deleting a file unregisters its tables, so this is the residue of a cleanup
// that did not complete -- rare, and the one row a reader has to be told about
// rather than shown plainly.
const orphanedRegistration: TableRegistration = {
  id: "reg_a04e12",
  source_kind: "resource",
  source_id: "res-deleted",
  connection: "acme-scratch",
  catalog: "scratch",
  schema: "uploads",
  table: "analyst_q1_promo_codes",
  location: "s3://acme-platform/resources/global/reference/v/rev-1/",
  columns: [
    { name: "code", type: "VARCHAR" },
    { name: "discount_pct", type: "VARCHAR" },
  ],
  registered_by: "alice@example.com",
  registered_at: "2026-07-02T11:05:00Z",
  query_table: "scratch.uploads.analyst_q1_promo_codes",
  stale: true,
  follow: false,
  repair: false,
};

/** scratchTableRows is every registration the listing spans, newest first. */
function scratchTableRows(): ScratchTable[] {
  const perSource = Object.values(mockTableRegistrations).flat();
  const all = [...perSource, orphanedRegistration];
  return all
    .map(asScratchTable)
    .sort((a, b) => b.registered_at.localeCompare(a.registered_at));
}

/** asScratchTable adds what only a cross-source read can answer. */
function asScratchTable(reg: TableRegistration): ScratchTable {
  const source = scratchTableSources[reg.source_id];
  return {
    ...reg,
    source: {
      kind: reg.source_kind,
      id: reg.source_id,
      name: source?.name,
      missing: !source,
    },
    can_unregister: Boolean(source?.canModify) && reg.registered_by === "alice@example.com",
  };
}

/** mockScratchTableList serves one page of the listing, with its facets. */
export function mockScratchTableList(url: URL): {
  data: ScratchTable[];
  total: number;
  page: number;
  per_page: number;
} {
  const kind = url.searchParams.get("kind") ?? "";
  const connection = url.searchParams.get("connection") ?? "";
  const q = (url.searchParams.get("q") ?? "").toLowerCase();
  const perPage = Number(url.searchParams.get("per_page")) || 25;
  const page = Number(url.searchParams.get("page")) || 1;

  const matched = scratchTableRows().filter(
    (row) =>
      (!kind || row.source_kind === kind) &&
      (!connection || row.connection === connection) &&
      (!q || row.query_table.toLowerCase().includes(q)),
  );
  const start = (page - 1) * perPage;
  return { data: matched.slice(start, start + perPage), total: matched.length, page, per_page: perPage };
}

/** mockScratchTable reads one registration by id, or undefined for a miss. */
export function mockScratchTable(id: string): ScratchTable | undefined {
  return scratchTableRows().find((row) => row.id === id);
}
