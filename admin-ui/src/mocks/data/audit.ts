import type {
  AuditEvent,
  TimeseriesBucket,
  BreakdownEntry,
  Overview,
  PerformanceStats,
} from "@/api/types";

const tools = [
  "trino_query",
  "trino_describe_table",
  "trino_list_catalogs",
  "datahub_search",
  "datahub_get_entity",
  "datahub_get_schema",
  "s3_list_objects",
  "s3_get_object",
];

const users = ["alice@corp.com", "bob@corp.com", "carol@corp.com", "dave@corp.com", "eve@corp.com"];
const personas = ["analyst", "admin", "engineer"];
const toolkitKinds = ["trino", "datahub", "s3"];

function randomItem<T>(arr: T[]): T {
  return arr[Math.floor(Math.random() * arr.length)]!;
}

function generateEvents(count: number): AuditEvent[] {
  const events: AuditEvent[] = [];
  const now = Date.now();

  for (let i = 0; i < count; i++) {
    const tool = randomItem(tools);
    const success = Math.random() > 0.08;
    const timestamp = new Date(now - Math.random() * 24 * 60 * 60 * 1000);
    const kind = tool.startsWith("trino")
      ? "trino"
      : tool.startsWith("datahub")
        ? "datahub"
        : "s3";

    events.push({
      id: `evt-${String(i).padStart(4, "0")}`,
      timestamp: timestamp.toISOString(),
      duration_ms: Math.floor(Math.random() * 500) + 10,
      request_id: `req-${crypto.randomUUID().slice(0, 8)}`,
      session_id: `sess-${Math.floor(Math.random() * 10)}`,
      user_id: randomItem(users),
      user_email: "",
      persona: randomItem(personas),
      tool_name: tool,
      toolkit_kind: kind,
      toolkit_name: kind === "trino" ? "production" : kind === "datahub" ? "primary" : "data-lake",
      connection: kind === "trino" ? "prod-trino" : kind === "datahub" ? "prod-datahub" : "prod-s3",
      parameters: { catalog: "hive", schema: "default" },
      success,
      error_message: success ? "" : "query timeout exceeded",
      response_chars: Math.floor(Math.random() * 5000) + 100,
      request_chars: Math.floor(Math.random() * 500) + 50,
      content_blocks: Math.floor(Math.random() * 3) + 1,
      transport: "http",
      source: "mcp",
      enrichment_applied: Math.random() > 0.3,
      authorized: true,
    });
  }

  return events.sort(
    (a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime(),
  );
}

export const mockAuditEvents = generateEvents(75);

function generateTimeseries(): TimeseriesBucket[] {
  const buckets: TimeseriesBucket[] = [];
  const now = new Date();
  now.setMinutes(0, 0, 0);

  for (let i = 23; i >= 0; i--) {
    const bucket = new Date(now.getTime() - i * 60 * 60 * 1000);
    const count = Math.floor(Math.random() * 30) + 5;
    const errorCount = Math.floor(Math.random() * 3);
    buckets.push({
      bucket: bucket.toISOString(),
      count,
      success_count: count - errorCount,
      error_count: errorCount,
      avg_duration_ms: Math.random() * 200 + 20,
    });
  }

  return buckets;
}

export const mockTimeseries = generateTimeseries();

export const mockToolBreakdown: BreakdownEntry[] = [
  { dimension: "trino_query", count: 245, success_rate: 0.94, avg_duration_ms: 156.3 },
  { dimension: "datahub_search", count: 189, success_rate: 0.99, avg_duration_ms: 45.2 },
  { dimension: "trino_describe_table", count: 134, success_rate: 0.97, avg_duration_ms: 89.1 },
  { dimension: "s3_list_objects", count: 98, success_rate: 1.0, avg_duration_ms: 32.5 },
  { dimension: "datahub_get_entity", count: 76, success_rate: 0.96, avg_duration_ms: 67.8 },
  { dimension: "trino_list_catalogs", count: 54, success_rate: 1.0, avg_duration_ms: 12.4 },
  { dimension: "s3_get_object", count: 43, success_rate: 0.93, avg_duration_ms: 234.1 },
  { dimension: "datahub_get_schema", count: 31, success_rate: 1.0, avg_duration_ms: 55.6 },
];

export const mockUserBreakdown: BreakdownEntry[] = [
  { dimension: "alice@corp.com", count: 312, success_rate: 0.96, avg_duration_ms: 98.4 },
  { dimension: "bob@corp.com", count: 245, success_rate: 0.94, avg_duration_ms: 112.3 },
  { dimension: "carol@corp.com", count: 178, success_rate: 0.99, avg_duration_ms: 67.8 },
  { dimension: "dave@corp.com", count: 98, success_rate: 0.92, avg_duration_ms: 145.2 },
  { dimension: "eve@corp.com", count: 37, success_rate: 1.0, avg_duration_ms: 34.5 },
];

export const mockOverview: Overview = {
  total_calls: 870,
  success_rate: 0.961,
  avg_duration_ms: 89.4,
  unique_users: 5,
  unique_tools: 8,
  enrichment_rate: 0.72,
  error_count: 34,
};

export const mockPerformance: PerformanceStats = {
  p50_ms: 45.2,
  p95_ms: 234.1,
  p99_ms: 456.8,
  avg_ms: 89.4,
  max_ms: 1234.5,
  avg_response_chars: 2456.3,
  avg_request_chars: 312.7,
};

export { tools as mockToolNames, users as mockUsers, toolkitKinds as mockToolkitKinds };
