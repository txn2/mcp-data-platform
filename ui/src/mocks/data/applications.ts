import type { ApplicationPreparePlan, ApplicationVariable } from "@/api/portal/hooks/applicationEnvironments";
import type {
  Application,
  ApplicationCandidate,
  ApplicationDetail,
  ApplicationPart,
  ApplicationPartView,
  ApplicationRelease,
  ApplicationSchemaView,
} from "@/api/portal/hooks/applications";
import { warehouseApplication, warehouseCatalog, warehouseFindings } from "./applicationWarehouse";

// Three applications (#2411): "shop", released twice with a rollback between,
// made of a schema, two scripts, an asset and a webhook source; "intake",
// recorded but never released; and "warehouse", twenty automations over a
// schema of twenty tables, drawn as a large application (#2700,
// applicationWarehouse.ts). The script, asset and source ids are ones the
// other mocks serve, so a part reads as something that exists. shop's schema
// is a part of kind schema (#2539), listed last.

export const mockApplications: Application[] = [
  {
    id: "app-shop",
    name: "shop",
    description: "The storefront, its order API and the daily sales report.",
    owner_email: "sarah.chen@example.com",
    connection: "acme-shop",
    schema: "shop",
    schema_adopted_version: 0,
    current_release: 3,
    created_by: "sarah.chen@example.com",
    created_at: "2025-01-10T09:00:00Z",
    updated_at: "2025-01-22T16:30:00Z",
    parts: [
      { kind: "script", id: "script-001", added_by: "sarah.chen@example.com", added_at: "2025-01-10T09:05:00Z" },
      { kind: "script", id: "script-002", added_by: "sarah.chen@example.com", added_at: "2025-01-10T09:06:00Z" },
      { kind: "asset", id: "ast-001", added_by: "sarah.chen@example.com", added_at: "2025-01-12T11:00:00Z" },
      { kind: "source", id: "billing-events", added_by: "admin@example.com", added_at: "2025-01-15T14:00:00Z" },
      { kind: "schema", id: "acme-shop/shop", added_by: "sarah.chen@example.com", added_at: "2025-01-10T09:00:00Z" },
      { kind: "site", id: "shop-web/dev", added_by: "sarah.chen@example.com", added_at: "2025-01-16T09:00:00Z", release_mode: "coupled" },
      { kind: "site", id: "shop-web/prod", added_by: "sarah.chen@example.com", added_at: "2025-01-16T09:00:00Z", release_mode: "coupled" },
      { kind: "site", id: "shop-web/staging", added_by: "sarah.chen@example.com", added_at: "2025-01-16T09:00:00Z", release_mode: "linked" },
    ],
    environments: [
      { name: "dev", release: 3, requires_approval: false, schema: "shop_dev", updated_by: "sarah.chen@example.com", updated_at: "2025-01-22T16:30:00Z" },
      { name: "prod", release: 3, requires_approval: true, schema: "shop", updated_by: "admin@example.com", updated_at: "2025-01-22T16:30:00Z" },
    ],
  },
  {
    id: "app-intake",
    name: "intake",
    description: "The customer intake form and where its submissions are filed.",
    owner_email: "j.martinez@example.com",
    connection: "",
    schema: "",
    schema_adopted_version: 0,
    current_release: 0,
    created_by: "j.martinez@example.com",
    created_at: "2025-01-20T10:00:00Z",
    updated_at: "2025-01-20T10:00:00Z",
    parts: [
      { kind: "script", id: "script-004", added_by: "j.martinez@example.com", added_at: "2025-01-20T10:02:00Z" },
    ],
    environments: [
      { name: "prod", release: 0, requires_approval: false, schema: "", updated_by: "j.martinez@example.com", updated_at: "2025-01-20T10:00:00Z" },
    ],
  },
  warehouseApplication,
];

const shopReleases: ApplicationRelease[] = [
  {
    number: 3,
    action: "rollback",
    migration_version: 2,
    restored: 1,
    changes: {
      parts: [{ kind: "script", part_id: "script-001", from: 5, to: 4 }],
      applied: [],
      reverted: [],
      migrations_kept: "Migration 3 stays applied: a rollback does not revert the schema.",
    },
    note: "The new report total was wrong.",
    created_by: "admin@example.com",
    created_at: "2025-01-22T16:30:00Z",
    pins: [
      { kind: "script", part_id: "script-001", version: 4 },
      { kind: "script", part_id: "script-002", version: 2 },
      { kind: "asset", part_id: "ast-001", version: 3 },
      { kind: "site", part_id: "shop-web/dev", version: 31 },
      { kind: "site", part_id: "shop-web/prod", version: 31 },
    ],
  },
  {
    number: 2,
    action: "release",
    migration_version: 3,
    changes: {
      parts: [
        { kind: "script", part_id: "script-001", from: 4, to: 5 },
        { kind: "asset", part_id: "ast-001", from: 2, to: 3 },
      ],
      applied: [3],
      reverted: [],
    },
    note: "Adds the refunds column to the report.",
    created_by: "admin@example.com",
    created_at: "2025-01-21T12:00:00Z",
    pins: [
      { kind: "script", part_id: "script-001", version: 5 },
      { kind: "script", part_id: "script-002", version: 2 },
      { kind: "asset", part_id: "ast-001", version: 3 },
    ],
  },
  {
    number: 1,
    action: "release",
    migration_version: 2,
    changes: {
      parts: [
        { kind: "script", part_id: "script-001", from: 0, to: 4 },
        { kind: "script", part_id: "script-002", from: 0, to: 2 },
        { kind: "asset", part_id: "ast-001", from: 0, to: 2 },
      ],
      applied: [1, 2],
      reverted: [],
    },
    created_by: "admin@example.com",
    created_at: "2025-01-15T15:00:00Z",
    pins: [
      { kind: "script", part_id: "script-001", version: 4 },
      { kind: "script", part_id: "script-002", version: 2 },
      { kind: "asset", part_id: "ast-001", version: 2 },
    ],
  },
];

/**
 * What each script, asset and webhook source the mocks know is called and
 * what its row says (#2527), keyed kind/id. A part missing from it reads as
 * deleted, which is what the server answers for one that is gone.
 */
export const mockPartCatalog: Record<string, Omit<ApplicationPartView, "state" | "added_by" | "added_at">> = {
  "script/script-001": {
    kind: "script", id: "script-001", name: "Daily sales report", description: "Summarizes yesterday's sales by region",
    latest_version: 5, owner_email: "sarah.chen@example.com", updated_at: "2025-01-21T11:00:00Z",
    schedules: [{ cron_spec: "0 7 * * *", timezone: "UTC", enabled: true }],
  },
  "script/script-002": {
    kind: "script", id: "script-002", name: "Order intake API", description: "Answers the storefront's order calls",
    latest_version: 2, owner_email: "sarah.chen@example.com", updated_at: "2025-01-12T11:00:00Z",
  },
  "script/script-004": {
    kind: "script", id: "script-004", name: "Intake form", description: "Files each customer intake submission",
    latest_version: 1, owner_email: "j.martinez@example.com", updated_at: "2025-01-20T10:00:00Z", event_sources: ["intake-form"],
  },
  "script/script-005": {
    kind: "script", id: "script-005", name: "Monthly revenue close", latest_version: 3,
    owner_email: "sarah.chen@example.com", updated_at: "2025-01-24T09:00:00Z",
  },
  "script/script-006": {
    kind: "script", id: "script-006", name: "Revenue forecast refresh", latest_version: 1,
    owner_email: "sarah.chen@example.com", updated_at: "2025-01-18T09:00:00Z",
  },
  "asset/ast-001": {
    kind: "asset", id: "ast-001", name: "Sales dashboard", latest_version: 3, content_type: "text/html",
    owner_email: "sarah.chen@example.com", updated_at: "2025-01-21T12:00:00Z",
  },
  "asset/ast-002": {
    kind: "asset", id: "ast-002", name: "Revenue by region", latest_version: 4, content_type: "text/jsx",
    owner_email: "sarah.chen@example.com", updated_at: "2025-01-23T12:00:00Z",
  },
  "source/billing-events": { kind: "source", id: "billing-events", name: "billing-events", latest_version: null, requests_last_day: 1284 },
  "site/storefront/dev": { kind: "site", id: "storefront/dev", name: "storefront / dev", latest_version: null },
  "site/shop-web/preview": { kind: "site", id: "shop-web/preview", name: "shop-web / preview", latest_version: null },
  "source/revenue-events": { kind: "source", id: "revenue-events", name: "revenue-events", latest_version: null, requests_last_day: 312 },
};

/** partView is a part as the detail route reads it from the catalog; a schema is named by its id. */
export function partView(p: ApplicationPart): ApplicationPartView {
  if (p.kind === "schema" || p.kind === "site") {
    return { kind: p.kind, id: p.id, state: "ok", name: p.id, latest_version: null, added_by: p.added_by, added_at: p.added_at };
  }
  const known = mockPartCatalog[`${p.kind}/${p.id}`] ?? warehouseCatalog[`${p.kind}/${p.id}`];
  return known
    ? { ...known, state: "ok", added_by: p.added_by, added_at: p.added_at }
    : { kind: p.kind, id: p.id, state: "deleted", latest_version: null, added_by: p.added_by, added_at: p.added_at };
}

/** candidateOf is a catalog entry as the candidates route lists it. */
export function candidateOf(v: Omit<ApplicationPartView, "state" | "added_by" | "added_at">): ApplicationCandidate {
  return {
    kind: v.kind, id: v.id, name: v.name ?? v.id, owner_email: v.owner_email,
    content_type: v.content_type, updated_at: v.updated_at,
  };
}

/**
 * schemaViewOf is a newly added schema as the detail route shows it: the
 * platform grants its run role on it, so nothing is owed (#2709).
 */
export function schemaViewOf(connection: string, schema: string): ApplicationSchemaView {
  return {
    connection, schema, migration_version: 0, released_version: 0, pending: [], adopted_version: 0,
    run_role: `${schema}_run`, write_role: "",
    environments: [{ environment: "prod", schema, migration_version: 0, pending: [] }],
  };
}

/**
 * What shop's diagram finds (#2540): a table its scripts read that no part
 * writes. Advice, not a refusal: the release is not held back for it.
 */
export const SHOP_UNWRITTEN = "table shop.refunds is read by the application's scripts and written by no part";

export const mockApplicationDetails: Record<string, ApplicationDetail> = {
  "app-shop": {
    application: mockApplications[0]!,
    parts: mockApplications[0]!.parts.map(partView),
    current_release: shopReleases[0]!,
    pending: [{ kind: "script", part_id: "script-001", from: 4, to: 5 }],
    migrations: { applied: 3, pending: [4] },
    schema: {
      connection: "acme-shop", schema: "shop", migration_version: 3, released_version: 2, pending: [4],
      adopted_version: 0, run_role: "shop_run", write_role: "",
      environments: [
        { environment: "dev", schema: "shop_dev", migration_version: 3, pending: [4] },
        { environment: "prod", schema: "shop", migration_version: 3, pending: [4] },
      ],
    },
    problems: [SHOP_UNWRITTEN],
    findings: [{ kind: "unwritten_table", node: "table:acme-shop/shop.refunds", message: SHOP_UNWRITTEN, blocks: false }],
    releases: shopReleases,
    site_parts: [
      { part_id: "shop-web/dev", release_mode: "coupled", serves: 31, step: 32, contract: "", compatible: true },
      { part_id: "shop-web/prod", release_mode: "coupled", serves: 29, step: 31, contract: "", compatible: true },
      { part_id: "shop-web/staging", release_mode: "linked", serves: 30, step: 31, contract: "", compatible: true },
    ],
    prepare: { scripts: 0, libraries: 0, migrations: 0 },
  },
  "app-intake": {
    application: mockApplications[1]!,
    parts: mockApplications[1]!.parts.map(partView),
    current_release: null,
    pending: [{ kind: "script", part_id: "script-004", from: 0, to: 1 }],
    problems: ["script-004 has no saved version that passed its tests"],
    findings: [],
    releases: [],
  },
  "app-warehouse": {
    application: warehouseApplication,
    parts: warehouseApplication.parts.map(partView),
    current_release: null,
    pending: warehouseApplication.parts
      .filter((p) => p.kind === "script")
      .map((p) => ({ kind: p.kind, part_id: p.id, from: 0, to: 1 })),
    schema: {
      connection: "acme-shop", schema: "wh", migration_version: 1, released_version: 0, pending: [1],
      adopted_version: 0, run_role: "wh_run", write_role: "wh_write", note: "",
      environments: [{ environment: "prod", schema: "wh", migration_version: 1, pending: [] }],
    },
    prepare: { scripts: 2, libraries: 1, migrations: 1 },
    problems: warehouseFindings.map((f) => f.message),
    findings: warehouseFindings,
    releases: [],
  },
};

/** A bundle as the shop export writes it, for the import dialog's tests. */
export const mockApplicationBundle = {
  format: "mcp-data-platform/application",
  format_version: 1,
  application: { name: "shop", description: "The storefront, its order API and the daily sales report." },
  release: 2,
  exported_at: "2025-01-21T12:05:00Z",
  scripts: [],
  assets: [],
  sources: ["billing-events"],
  names: {
    connections: ["acme-shop"],
    secrets: ["stripe-key"],
    sources: ["billing-events"],
    bot_checks: [],
  },
};

/** The shop application's variables (#2696). */
export const mockApplicationVariables: Record<string, ApplicationVariable[]> = {
  "app-shop": [
    { environment: "", name: "API_BASE", value: "https://sandbox.pay.example.com", updated_by: "sarah.chen@example.com", updated_at: "2025-01-20T10:00:00Z" },
    { environment: "prod", name: "API_BASE", value: "https://pay.example.com", updated_by: "admin@example.com", updated_at: "2025-01-21T10:00:00Z" },
    { environment: "dev", name: "PROCESSOR_KEY", value: "{{secret:processor-sandbox}}", updated_by: "sarah.chen@example.com", updated_at: "2025-01-20T10:00:00Z" },
    { environment: "prod", name: "PROCESSOR_KEY", value: "{{secret:processor-live}}", updated_by: "admin@example.com", updated_at: "2025-01-21T10:00:00Z" },
  ],
};

/**
 * What prepare changes in warehouse (#2709): two automations and a library
 * whose SQL names the schema wh, and a pending migration that also grants a
 * role. Each changed script replays its recorded runs the same.
 */
export const mockWarehousePrepare: ApplicationPreparePlan = {
  application: "warehouse",
  schema: "wh",
  counts: { scripts: 2, libraries: 1, migrations: 1 },
  ready: false,
  saved: false,
  values: [{ kind: "script", name: "Receive stock", line: 31, text: "wh.receipts" }],
  files: [
    {
      kind: "script", id: "wh-01", name: "Receive stock", version: 17, names: 2, replayed: ["run_1001", "run_1002", "run_1003"], differences: [],
      before: 'def main():\n    rows = platform.query("acme-shop", "SELECT * FROM wh.receipt", {})\n    platform.execute("acme-shop", "INSERT INTO wh.stock (sku) VALUES (:sku)", rows[0])\n',
      after: 'def main():\n    rows = platform.query("acme-shop", "SELECT * FROM receipt", {})\n    platform.execute("acme-shop", "INSERT INTO stock (sku) VALUES (:sku)", rows[0])\n',
    },
    {
      kind: "script", id: "wh-12", name: "Post refunds", version: 4, names: 1, replayed: ["run_2001", "run_2002"], differences: [],
      before: 'REFUNDS = "INSERT INTO wh.refund (order_id) VALUES (:o)"\n',
      after: 'REFUNDS = "INSERT INTO refund (order_id) VALUES (:o)"\n',
    },
    {
      kind: "library", id: "lib-wh-sql", name: "wh-sql", version: 2, names: 1, replayed: [], differences: [],
      before: 'ORDERS = "SELECT * FROM wh.order_status"\n',
      after: 'ORDERS = "SELECT * FROM order_status"\n',
    },
    {
      kind: "migration", id: "2", name: "refunds", version: 2, names: 2, replayed: [], differences: [],
      before: "CREATE TABLE wh.refund_reason (code text PRIMARY KEY);\nGRANT SELECT ON wh.refund_reason TO wh_write;",
      after: "CREATE TABLE refund_reason (code text PRIMARY KEY);",
    },
  ],
};
