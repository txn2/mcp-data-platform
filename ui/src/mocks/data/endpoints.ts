import type { AdminEndpoint, AdminEndpointDetail, EndpointBucket, EndpointRange } from "@/api/admin/types";

// The admin Endpoints page's data (#2208): two bindings, one public with a
// flood refused at its bot check, one signed in.

const HOUR = 3600 * 1000;

function hoursAgo(n: number): string {
  return new Date(Date.now() - n * HOUR).toISOString();
}

export const mockAdminEndpoints: AdminEndpoint[] = [
  {
    id: "ep-001",
    name: "contact",
    url: "https://data.example.com/endpoints/contact",
    methods: ["POST"],
    path: "",
    auth_mode: "public",
    open_reads: false,
    enabled: true,
    timeout_seconds: 10,
    max_body_bytes: 65536,
    rate_limit: 120,
    caller_rate_limit: 30,
    updated_by: "admin@example.com",
    updated_at: hoursAgo(50),
    script: { id: "script-002", name: "dormant-accounts", display_name: "Contact Form", owner: "marcus.webb@example.com" },
    traffic: {
      requests: 412,
      by_status_class: { "2xx": 398, "3xx": 0, "4xx": 11, "5xx": 3 },
      failures: 3,
      p50_ms: 84,
      p95_ms: 310,
      callers: 0,
      last_request_at: hoursAgo(0.2),
    },
    refused: 1873,
    refused_by_reason: { bot_check: 1840, rate_limit: 33 },
  },
  {
    id: "ep-002",
    name: "store-lookup",
    url: "https://data.example.com/endpoints/store-lookup",
    methods: ["GET"],
    path: "/{id}",
    auth_mode: "signed_in",
    open_reads: false,
    enabled: true,
    timeout_seconds: 5,
    max_body_bytes: 4096,
    rate_limit: 600,
    caller_rate_limit: 120,
    updated_by: "admin@example.com",
    updated_at: hoursAgo(200),
    script: { id: "script-001", name: "daily-sales-report", display_name: "Daily Sales Report", owner: "sarah.chen@example.com" },
    traffic: {
      requests: 1290,
      by_status_class: { "2xx": 1271, "3xx": 0, "4xx": 19, "5xx": 0 },
      failures: 0,
      p50_ms: 22,
      p95_ms: 61,
      callers: 7,
      last_request_at: hoursAgo(0.05),
    },
    refused: 4,
    refused_by_reason: { caller_rate_limit: 4 },
  },
];

/** mockEndpointDetail is one endpoint's page over range. */
export function mockEndpointDetail(name: string, range: EndpointRange): AdminEndpointDetail | undefined {
  const e = mockAdminEndpoints.find((x) => x.name === name);
  if (!e) return undefined;
  const step = range === "week" ? 6 : 1;
  const count = range === "week" ? 28 : 24;
  const series: EndpointBucket[] = Array.from({ length: count }, (_, i) => {
    const start = new Date(Math.floor((Date.now() - (count - 1 - i) * step * HOUR) / (step * HOUR)) * step * HOUR);
    const wave = Math.round(10 + 8 * Math.sin(i / 3));
    return {
      start: start.toISOString(),
      "2xx": wave,
      "3xx": 0,
      "4xx": i % 5 === 0 ? 2 : 0,
      "5xx": i === count - 3 ? 1 : 0,
      refused: e.auth_mode === "public" && i > count - 5 ? 400 : 0,
    };
  });
  return {
    ...e,
    range,
    since: hoursAgo(range === "week" ? 168 : 24),
    bucket_seconds: step * 3600,
    series,
    top_callers:
      e.auth_mode === "signed_in"
        ? [
            { caller: "sarah.chen@example.com", requests: 610 },
            { caller: "marcus.webb@example.com", requests: 402 },
          ]
        : [],
    failures:
      e.traffic.failures > 0
        ? [
            {
              id: "f-1",
              version: 3,
              method: "POST",
              status: 500,
              error: "handle(req) failed: the CRM answered 503",
              duration_ms: 2100,
              created_at: hoursAgo(3),
            },
          ]
        : [],
  };
}
