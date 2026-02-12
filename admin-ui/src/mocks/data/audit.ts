import type {
  AuditEvent,
  TimeseriesBucket,
  BreakdownEntry,
  Overview,
  PerformanceStats,
} from "@/api/types";

// ---------------------------------------------------------------------------
// Seeded PRNG (mulberry32) — deterministic mock data across page loads
// ---------------------------------------------------------------------------
function mulberry32(seed: number): () => number {
  let s = seed | 0;
  return () => {
    s = (s + 0x6d2b79f5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const rand = mulberry32(20240215); // fixed seed for screenshot consistency

function seededItem<T>(arr: T[]): T {
  return arr[Math.floor(rand() * arr.length)]!;
}

function seededInt(min: number, max: number): number {
  return Math.floor(rand() * (max - min + 1)) + min;
}

// ---------------------------------------------------------------------------
// ACME Corporation — Users, Personas, Tools, Connections
// ---------------------------------------------------------------------------

interface AcmeUser {
  email: string;
  persona: string;
  weight: number; // relative activity level
}

const acmeUsers: AcmeUser[] = [
  { email: "sarah.chen@acme-corp.com", persona: "admin", weight: 8 },
  { email: "marcus.johnson@acme-corp.com", persona: "data-engineer", weight: 15 },
  { email: "rachel.thompson@acme-corp.com", persona: "inventory-analyst", weight: 12 },
  { email: "david.park@acme-corp.com", persona: "regional-director", weight: 6 },
  { email: "jennifer.martinez@acme-corp.com", persona: "finance-executive", weight: 5 },
  { email: "kevin.wilson@acme-corp.com", persona: "store-manager", weight: 7 },
  { email: "amanda.lee@acme-corp.com", persona: "data-engineer", weight: 14 },
  { email: "carlos.rodriguez@acme-corp.com", persona: "regional-director", weight: 6 },
  { email: "emily.watson@acme-corp.com", persona: "inventory-analyst", weight: 10 },
  { email: "brian.taylor@acme-corp.com", persona: "finance-executive", weight: 4 },
  { email: "lisa.chang@acme-corp.com", persona: "data-engineer", weight: 11 },
  { email: "mike.davis@acme-corp.com", persona: "store-manager", weight: 3 },
];

function weightedUser(): AcmeUser {
  const totalWeight = acmeUsers.reduce((s, u) => s + u.weight, 0);
  let r = rand() * totalWeight;
  for (const u of acmeUsers) {
    r -= u.weight;
    if (r <= 0) return u;
  }
  return acmeUsers[0]!;
}

interface ToolDef {
  name: string;
  connection: string;
  kind: string;
  toolkit: string;
  weight: number; // usage frequency
}

const toolDefs: ToolDef[] = [
  // acme-warehouse (trino) — 35% of traffic
  { name: "trino_query", connection: "acme-warehouse", kind: "trino", toolkit: "acme-warehouse", weight: 22 },
  { name: "trino_describe_table", connection: "acme-warehouse", kind: "trino", toolkit: "acme-warehouse", weight: 7 },
  { name: "trino_list_tables", connection: "acme-warehouse", kind: "trino", toolkit: "acme-warehouse", weight: 3 },
  { name: "trino_list_schemas", connection: "acme-warehouse", kind: "trino", toolkit: "acme-warehouse", weight: 2 },
  { name: "trino_explain", connection: "acme-warehouse", kind: "trino", toolkit: "acme-warehouse", weight: 1 },
  // acme-staging (trino) — 5% of traffic
  { name: "trino_query", connection: "acme-staging", kind: "trino", toolkit: "acme-staging", weight: 4 },
  { name: "trino_describe_table", connection: "acme-staging", kind: "trino", toolkit: "acme-staging", weight: 1 },
  // acme-catalog (datahub) — 25% of traffic
  { name: "datahub_search", connection: "acme-catalog", kind: "datahub", toolkit: "acme-catalog", weight: 12 },
  { name: "datahub_get_entity", connection: "acme-catalog", kind: "datahub", toolkit: "acme-catalog", weight: 6 },
  { name: "datahub_get_schema", connection: "acme-catalog", kind: "datahub", toolkit: "acme-catalog", weight: 3 },
  { name: "datahub_get_lineage", connection: "acme-catalog", kind: "datahub", toolkit: "acme-catalog", weight: 2 },
  { name: "datahub_get_column_lineage", connection: "acme-catalog", kind: "datahub", toolkit: "acme-catalog", weight: 1 },
  // acme-catalog-staging (datahub) — 3% of traffic
  { name: "datahub_search", connection: "acme-catalog-staging", kind: "datahub", toolkit: "acme-catalog-staging", weight: 2 },
  { name: "datahub_get_entity", connection: "acme-catalog-staging", kind: "datahub", toolkit: "acme-catalog-staging", weight: 1 },
  // acme-data-lake (s3) — 8% of traffic
  { name: "s3_list_objects", connection: "acme-data-lake", kind: "s3", toolkit: "acme-data-lake", weight: 4 },
  { name: "s3_get_object", connection: "acme-data-lake", kind: "s3", toolkit: "acme-data-lake", weight: 3 },
  { name: "s3_list_buckets", connection: "acme-data-lake", kind: "s3", toolkit: "acme-data-lake", weight: 1 },
  // acme-reports (s3) — 4% of traffic
  { name: "s3_list_objects", connection: "acme-reports", kind: "s3", toolkit: "acme-reports", weight: 2 },
  { name: "s3_get_object", connection: "acme-reports", kind: "s3", toolkit: "acme-reports", weight: 2 },
];

const totalToolWeight = toolDefs.reduce((s, t) => s + t.weight, 0);

function weightedTool(): ToolDef {
  let r = rand() * totalToolWeight;
  for (const t of toolDefs) {
    r -= t.weight;
    if (r <= 0) return t;
  }
  return toolDefs[0]!;
}

// ---------------------------------------------------------------------------
// Business-hours timestamp weighting
// ---------------------------------------------------------------------------

// Relative activity by hour (0-23). Peak at 9-11am and 2-4pm.
const hourlyPattern = [
  1, 1, 0, 0, 0, 1, 2, 5, 12, 18, 16, 14, // 0am-11am
  10, 11, 15, 17, 14, 8, 4, 3, 2, 2, 1, 1,  // 12pm-11pm
];
const hourlyTotal = hourlyPattern.reduce((s, v) => s + v, 0);

function businessHourTimestamp(baseDate: Date): Date {
  // Pick an hour weighted by business pattern
  let r = rand() * hourlyTotal;
  let hour = 0;
  for (let h = 0; h < 24; h++) {
    r -= hourlyPattern[h]!;
    if (r <= 0) { hour = h; break; }
  }
  const minute = seededInt(0, 59);
  const second = seededInt(0, 59);
  const result = new Date(baseDate);
  result.setHours(hour, minute, second, seededInt(0, 999));
  return result;
}

// ---------------------------------------------------------------------------
// Realistic parameters & error messages per tool type
// ---------------------------------------------------------------------------

const trinoSchemas = ["retail", "inventory", "finance", "analytics", "staging"];
const trinoCatalogs = ["iceberg", "hive", "memory"];
const trinoTables = [
  "daily_sales", "store_transactions", "inventory_levels", "product_catalog",
  "customer_segments", "regional_performance", "supply_chain_orders",
  "price_adjustments", "return_rates", "employee_schedules",
];
const datahubQueries = [
  "daily_sales", "inventory", "customer", "revenue", "store performance",
  "supply chain", "product catalog", "regional", "forecast", "shrinkage",
];
const s3Buckets = [
  "acme-raw-transactions", "acme-analytics-output", "acme-ml-features",
  "acme-report-archive", "acme-data-exports",
];
const s3Prefixes = [
  "raw/2024/", "processed/daily/", "exports/regional/", "ml/features/",
  "reports/quarterly/", "archives/2023/",
];

function toolParameters(tool: ToolDef): Record<string, unknown> {
  switch (tool.name) {
    case "trino_query":
      return {
        sql: `SELECT * FROM ${seededItem(trinoSchemas)}.${seededItem(trinoTables)} LIMIT ${seededItem([100, 500, 1000])}`,
        catalog: seededItem(trinoCatalogs),
      };
    case "trino_describe_table":
      return {
        catalog: seededItem(trinoCatalogs),
        schema: seededItem(trinoSchemas),
        table: seededItem(trinoTables),
      };
    case "trino_list_tables":
      return { catalog: seededItem(trinoCatalogs), schema: seededItem(trinoSchemas) };
    case "trino_list_schemas":
      return { catalog: seededItem(trinoCatalogs) };
    case "trino_list_catalogs":
      return {};
    case "trino_explain":
      return {
        sql: `SELECT region, SUM(revenue) FROM ${seededItem(trinoSchemas)}.${seededItem(trinoTables)} GROUP BY region`,
      };
    case "datahub_search":
      return { query: seededItem(datahubQueries), limit: seededItem([10, 25, 50]) };
    case "datahub_get_entity":
      return { urn: `urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.${seededItem(trinoSchemas)}.${seededItem(trinoTables)},PROD)` };
    case "datahub_get_schema":
      return { urn: `urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.${seededItem(trinoSchemas)}.${seededItem(trinoTables)},PROD)` };
    case "datahub_get_lineage":
      return {
        urn: `urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.${seededItem(trinoSchemas)}.${seededItem(trinoTables)},PROD)`,
        direction: seededItem(["upstream", "downstream"]),
        depth: seededItem([1, 2, 3]),
      };
    case "datahub_get_column_lineage":
      return { urn: `urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.${seededItem(trinoTables)},PROD)` };
    case "s3_list_objects":
      return { bucket: seededItem(s3Buckets), prefix: seededItem(s3Prefixes) };
    case "s3_get_object":
      return { bucket: seededItem(s3Buckets), key: `${seededItem(s3Prefixes)}${seededItem(trinoTables)}.parquet` };
    case "s3_list_buckets":
      return {};
    default:
      return {};
  }
}

function errorMessage(tool: ToolDef): string {
  const trinoErrors = [
    "Query exceeded maximum execution time of 300s",
    "Query failed: Table does not exist",
    "Insufficient permissions on schema 'finance'",
    "Query cancelled by user",
    "Memory limit exceeded: 2.1GB of 2.0GB",
  ];
  const datahubErrors = [
    "Entity not found for URN",
    "Lineage depth exceeded maximum of 5 hops",
    "Search index temporarily unavailable",
    "Permission denied: restricted dataset",
    "GraphQL query timeout after 30s",
  ];
  const s3Errors = [
    "NoSuchKey: The specified key does not exist",
    "AccessDenied: User does not have s3:GetObject permission",
    "NoSuchBucket: The specified bucket does not exist",
    "SlowDown: Please reduce request rate",
  ];

  switch (tool.kind) {
    case "trino": return seededItem(trinoErrors);
    case "datahub": return seededItem(datahubErrors);
    case "s3": return seededItem(s3Errors);
    default: return "Unknown error";
  }
}

// Duration depends on tool type
function toolDuration(tool: ToolDef): number {
  switch (tool.name) {
    case "trino_query": return seededInt(45, 2800);
    case "trino_describe_table": return seededInt(20, 250);
    case "trino_explain": return seededInt(30, 400);
    case "trino_list_tables": return seededInt(10, 80);
    case "trino_list_schemas": return seededInt(8, 50);
    case "trino_list_catalogs": return seededInt(5, 30);
    case "datahub_search": return seededInt(25, 180);
    case "datahub_get_entity": return seededInt(30, 200);
    case "datahub_get_schema": return seededInt(25, 150);
    case "datahub_get_lineage": return seededInt(40, 350);
    case "datahub_get_column_lineage": return seededInt(50, 400);
    case "s3_list_objects": return seededInt(15, 100);
    case "s3_get_object": return seededInt(20, 500);
    case "s3_list_buckets": return seededInt(8, 40);
    default: return seededInt(10, 200);
  }
}

// ---------------------------------------------------------------------------
// Generate 200 audit events
// ---------------------------------------------------------------------------

function generateEvents(count: number): AuditEvent[] {
  const events: AuditEvent[] = [];
  const now = new Date();
  // Use today as the base date for all events (within 24h window)
  const baseDate = new Date(now.getFullYear(), now.getMonth(), now.getDate());

  for (let i = 0; i < count; i++) {
    const user = weightedUser();
    const tool = weightedTool();
    // 3.7% error rate → 96.3% success rate
    const success = rand() > 0.037;
    const duration = toolDuration(tool);
    const timestamp = businessHourTimestamp(baseDate);

    events.push({
      id: `evt-${String(i).padStart(4, "0")}`,
      timestamp: timestamp.toISOString(),
      duration_ms: duration,
      request_id: `req-${String(seededInt(10000000, 99999999))}`,
      session_id: `sess-${String(seededInt(100, 999))}`,
      user_id: user.email,
      user_email: user.email,
      persona: user.persona,
      tool_name: tool.name,
      toolkit_kind: tool.kind,
      toolkit_name: tool.toolkit,
      connection: tool.connection,
      parameters: toolParameters(tool),
      success,
      error_message: success ? "" : errorMessage(tool),
      response_chars: seededInt(200, 12000),
      request_chars: seededInt(50, 800),
      content_blocks: seededInt(1, 5),
      transport: "http",
      source: "mcp",
      enrichment_applied: rand() > 0.22, // 78% enrichment rate
      authorized: true,
    });
  }

  return events.sort(
    (a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime(),
  );
}

export const mockAuditEvents = generateEvents(200);

// ---------------------------------------------------------------------------
// Timeseries — 24 hourly buckets with business-hours bell curve
// ---------------------------------------------------------------------------

function generateTimeseries(): TimeseriesBucket[] {
  const buckets: TimeseriesBucket[] = [];
  const now = new Date();
  now.setMinutes(0, 0, 0);

  // Target ~4,827 total calls distributed by hourly pattern
  const targetTotal = 4827;
  const scale = targetTotal / hourlyTotal;

  for (let i = 23; i >= 0; i--) {
    const bucket = new Date(now.getTime() - i * 60 * 60 * 1000);
    const hour = bucket.getHours();
    const baseCount = Math.round(hourlyPattern[hour]! * scale);
    // Add slight jitter
    const count = Math.max(1, baseCount + seededInt(-3, 3));
    const errorCount = Math.round(count * 0.037 + (rand() - 0.5) * 2);
    const errors = Math.max(0, Math.min(errorCount, count));
    buckets.push({
      bucket: bucket.toISOString(),
      count,
      success_count: count - errors,
      error_count: errors,
      avg_duration_ms: seededInt(80, 200) + rand() * 40,
    });
  }

  return buckets;
}

export const mockTimeseries = generateTimeseries();

// ---------------------------------------------------------------------------
// Breakdowns — tool, user, persona, connection, toolkit
// ---------------------------------------------------------------------------

export const mockToolBreakdown: BreakdownEntry[] = [
  { dimension: "trino_query", count: 1689, success_rate: 0.943, avg_duration_ms: 245.7 },
  { dimension: "datahub_search", count: 869, success_rate: 0.991, avg_duration_ms: 67.3 },
  { dimension: "trino_describe_table", count: 531, success_rate: 0.978, avg_duration_ms: 89.4 },
  { dimension: "datahub_get_entity", count: 434, success_rate: 0.967, avg_duration_ms: 78.2 },
  { dimension: "s3_list_objects", count: 362, success_rate: 0.994, avg_duration_ms: 42.1 },
  { dimension: "s3_get_object", count: 338, success_rate: 0.953, avg_duration_ms: 156.8 },
  { dimension: "datahub_get_schema", count: 241, success_rate: 0.988, avg_duration_ms: 62.5 },
  { dimension: "trino_list_tables", count: 193, success_rate: 1.0, avg_duration_ms: 28.3 },
];

export const mockUserBreakdown: BreakdownEntry[] = [
  { dimension: "marcus.johnson@acme-corp.com", count: 724, success_rate: 0.958, avg_duration_ms: 167.2 },
  { dimension: "amanda.lee@acme-corp.com", count: 676, success_rate: 0.964, avg_duration_ms: 152.8 },
  { dimension: "rachel.thompson@acme-corp.com", count: 579, success_rate: 0.971, avg_duration_ms: 98.4 },
  { dimension: "lisa.chang@acme-corp.com", count: 531, success_rate: 0.952, avg_duration_ms: 178.3 },
  { dimension: "emily.watson@acme-corp.com", count: 483, success_rate: 0.983, avg_duration_ms: 87.6 },
  { dimension: "sarah.chen@acme-corp.com", count: 386, success_rate: 0.969, avg_duration_ms: 134.5 },
  { dimension: "kevin.wilson@acme-corp.com", count: 338, success_rate: 0.976, avg_duration_ms: 72.1 },
  { dimension: "david.park@acme-corp.com", count: 290, success_rate: 0.962, avg_duration_ms: 95.3 },
  { dimension: "carlos.rodriguez@acme-corp.com", count: 290, success_rate: 0.955, avg_duration_ms: 103.7 },
  { dimension: "jennifer.martinez@acme-corp.com", count: 241, success_rate: 0.979, avg_duration_ms: 68.9 },
  { dimension: "brian.taylor@acme-corp.com", count: 193, success_rate: 0.984, avg_duration_ms: 54.2 },
  { dimension: "mike.davis@acme-corp.com", count: 96, success_rate: 0.990, avg_duration_ms: 45.8 },
];

export const mockPersonaBreakdown: BreakdownEntry[] = [
  { dimension: "data-engineer", count: 1931, success_rate: 0.958, avg_duration_ms: 165.4 },
  { dimension: "inventory-analyst", count: 1062, success_rate: 0.977, avg_duration_ms: 93.1 },
  { dimension: "admin", count: 386, success_rate: 0.969, avg_duration_ms: 134.5 },
  { dimension: "store-manager", count: 434, success_rate: 0.980, avg_duration_ms: 64.7 },
  { dimension: "regional-director", count: 580, success_rate: 0.959, avg_duration_ms: 99.5 },
  { dimension: "finance-executive", count: 434, success_rate: 0.981, avg_duration_ms: 62.8 },
];

export const mockConnectionBreakdown: BreakdownEntry[] = [
  { dimension: "acme-warehouse", count: 2413, success_rate: 0.952, avg_duration_ms: 189.3 },
  { dimension: "acme-catalog", count: 1158, success_rate: 0.982, avg_duration_ms: 71.4 },
  { dimension: "acme-data-lake", count: 531, success_rate: 0.975, avg_duration_ms: 78.9 },
  { dimension: "acme-reports", count: 338, success_rate: 0.968, avg_duration_ms: 124.5 },
  { dimension: "acme-staging", count: 241, success_rate: 0.963, avg_duration_ms: 156.2 },
  { dimension: "acme-catalog-staging", count: 146, success_rate: 0.986, avg_duration_ms: 58.7 },
];

export const mockToolkitBreakdown: BreakdownEntry[] = [
  { dimension: "trino", count: 2654, success_rate: 0.954, avg_duration_ms: 185.2 },
  { dimension: "datahub", count: 1304, success_rate: 0.982, avg_duration_ms: 69.8 },
  { dimension: "s3", count: 869, success_rate: 0.972, avg_duration_ms: 95.4 },
];

// ---------------------------------------------------------------------------
// Overview & Performance — headline metrics
// ---------------------------------------------------------------------------

export const mockOverview: Overview = {
  total_calls: 4827,
  success_rate: 0.963,
  avg_duration_ms: 142,
  unique_users: 12,
  unique_tools: 20,
  enrichment_rate: 0.78,
  error_count: 178,
};

export const mockPerformance: PerformanceStats = {
  p50_ms: 67,
  p95_ms: 345,
  p99_ms: 892,
  avg_ms: 142,
  max_ms: 2847,
  avg_response_chars: 3842.6,
  avg_request_chars: 287.4,
};

// Re-export lists for handler filtering
export const mockToolNames = [...new Set(toolDefs.map((t) => t.name))];
export const mockUsers = acmeUsers.map((u) => u.email);
export const mockToolkitKinds = ["trino", "datahub", "s3"];
