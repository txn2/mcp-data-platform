import type {
  Application,
  ApplicationDiagramLink,
  ApplicationDiagramNode,
  ApplicationFinding,
  ApplicationPartView,
} from "@/api/portal/hooks/applications";

// "warehouse" (#2700): an application at the size the large diagram is for,
// twenty automations that between them read and write twenty tables of its
// schema, recorded and never released. Two tables have no writer, and two
// automations write a table named only when they run, so its findings and its
// one card per automation and connection show (#2699).

const SCRIPTS = [
  "Load orders", "Load order lines", "Sync customers", "Sync addresses", "Sync products",
  "Sync prices", "Sync stock", "Record stock moves", "Load warehouses", "Track order status",
  "Load payments", "Post refunds", "Load shipments", "Load shipment items", "Sync carriers",
  "Build invoices", "Build invoice lines", "Apply promos", "Daily summary", "Weekly report",
];

const TABLES = [
  "customer", "customer_address", "product", "product_price", "stock_level", "stock_move", "warehouse",
  "order", "order_line", "order_status", "payment", "refund", "refund_reason", "shipment",
  "shipment_item", "carrier", "invoice", "invoice_line", "promo", "sync_cursor",
];

// READS and WRITES are, per automation, the tables it reads and writes, by index.
const READS = [
  [0, 2], [7, 2], [19], [0], [19], [2, 18], [2, 6, 19], [2, 4, 6], [], [7, 13], [7, 0, 11], [7, 8, 10, 12], [7, 6, 15],
  [13, 8], [19], [7, 0, 10, 11, 1], [16, 8, 3], [2, 7], [7, 8, 10, 11, 13, 0], [7, 8, 9, 10, 11, 16, 17, 4],
];
const WRITES = [[7], [8], [0], [1], [2], [3], [4], [5], [6], [9], [10], [11, 9], [13], [14], [15], [16], [17], [18], [], []];

// RUNTIME are the automations that also write a table named only when they run.
const RUNTIME = [11, 14];

const CONNECTION = "acme-shop";
const SCHEMA = "wh";
const scriptId = (i: number) => `wh-${String(i + 1).padStart(2, "0")}`;
const tableNode = (j: number) => `table:${CONNECTION}/${SCHEMA}.${TABLES[j]}`;

export const warehouseApplication: Application = {
  id: "app-warehouse",
  name: "warehouse",
  description: "Keeps the warehouse schema in step with the store, and reports on it.",
  owner_email: "sarah.chen@example.com",
  connection: CONNECTION,
  schema: SCHEMA,
  schema_adopted_version: 0,
  current_release: 0,
  created_by: "sarah.chen@example.com",
  created_at: "2025-01-25T09:00:00Z",
  updated_at: "2025-01-25T09:00:00Z",
  parts: [
    ...SCRIPTS.map((_, i) => ({
      kind: "script" as const, id: scriptId(i), added_by: "sarah.chen@example.com", added_at: "2025-01-25T09:01:00Z",
    })),
    { kind: "source", id: "stock-events", added_by: "admin@example.com", added_at: "2025-01-25T09:02:00Z" },
    { kind: "schema", id: `${CONNECTION}/${SCHEMA}`, added_by: "sarah.chen@example.com", added_at: "2025-01-25T09:00:00Z" },
  ],
  environments: [
    { name: "prod", release: 0, requires_approval: false, updated_by: "sarah.chen@example.com", updated_at: "2025-01-25T09:00:00Z" },
  ],
};

/** warehouseCatalog is what warehouse's parts are called, kept out of the Add parts candidates. */
export const warehouseCatalog: Record<string, Omit<ApplicationPartView, "state" | "added_by" | "added_at">> = {
  ...Object.fromEntries(
    SCRIPTS.map((name, i) => [
      `script/${scriptId(i)}`,
      { kind: "script" as const, id: scriptId(i), name, latest_version: 1, owner_email: "sarah.chen@example.com", updated_at: "2025-01-25T09:00:00Z" },
    ]),
  ),
  "source/stock-events": { kind: "source", id: "stock-events", name: "stock-events", latest_version: null, requests_last_day: 96 },
};

const unwrittenMessage = (j: number) =>
  `table ${SCHEMA}.${TABLES[j]} is read by the application's scripts, and no part is known to write it; ` +
  "a script that writes a table named only when it runs may";

/** warehouseFindings is what warehouse's diagram finds, as the server words it. */
export const warehouseFindings: ApplicationFinding[] = [
  { kind: "unwritten_table", node: tableNode(12), message: unwrittenMessage(12), blocks: false },
  { kind: "unwritten_table", node: tableNode(19), message: unwrittenMessage(19), blocks: false },
  {
    kind: "runtime_writes",
    node: tableNode(12),
    message: `scripts ${SCRIPTS[11]} and ${SCRIPTS[14]} write tables named only when they run, which may be tables of ${SCHEMA} that no part is known to write`,
    blocks: false,
  },
];

/** warehouseReach is the tables warehouse's automations reach, and what starts them. */
export function warehouseReach(): { nodes: ApplicationDiagramNode[]; links: ApplicationDiagramLink[] } {
  const nodes: ApplicationDiagramNode[] = TABLES.map((label, j) => ({
    id: tableNode(j), kind: "table", label, group: "schema", part: false, computed: false, detail: [],
  }));
  const links: ApplicationDiagramLink[] = [{ from: "source:stock-events", to: "script:wh-07", kind: "event" }];
  SCRIPTS.forEach((_, i) => {
    for (const j of READS[i]!) links.push({ from: `script:${scriptId(i)}`, to: tableNode(j), kind: "reads" });
    for (const j of WRITES[i]!) links.push({ from: `script:${scriptId(i)}`, to: tableNode(j), kind: "writes" });
  });
  for (const i of RUNTIME) {
    const id = `computed:script:${scriptId(i)}:${CONNECTION}`;
    nodes.push({
      id, kind: "table", label: "table named when it runs", group: "outside", part: false, computed: true,
      detail: [`connection ${CONNECTION}`],
    });
    links.push({ from: `script:${scriptId(i)}`, to: id, kind: "writes" });
  }
  return { nodes, links };
}
