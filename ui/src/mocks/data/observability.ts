// Mock Prometheus responses for the observability PromQL proxy, used in
// MSW (VITE_MSW=true) dev and Playwright. The handler inspects the
// PromQL `query` string and returns a plausible vector/matrix so the
// API Gateway activity view renders real-looking data without a live
// Prometheus.
import type {
  PromVectorResponse,
  PromMatrixResponse,
} from "@/api/observability/types";

function vector(samples: { metric: Record<string, string>; value: number }[]): PromVectorResponse {
  const now = Math.floor(Date.now() / 1000);
  return {
    status: "success",
    data: {
      resultType: "vector",
      result: samples.map((s) => ({ metric: s.metric, value: [now, String(s.value)] })),
    },
  };
}

function scalarVector(value: number): PromVectorResponse {
  return vector([{ metric: {}, value }]);
}

// Simulated 3-node fleet (Kubernetes pod names + instance addresses) so the
// Health view renders multiple per-node cards. The real per-node queries
// return one series per scrape target with these same labels.
const NODE_PODS = [
  { pod: "mcp-data-platform-7d9f8c5b4-abcde", instance: "10.42.1.21:9464" },
  { pod: "mcp-data-platform-7d9f8c5b4-fghij", instance: "10.42.2.34:9464" },
  { pod: "mcp-data-platform-7d9f8c5b4-klmno", instance: "10.42.3.47:9464" },
];

// nodeVector returns one series per simulated node, carrying pod/instance
// labels, with the per-node value supplied positionally.
function nodeVector(values: number[]): PromVectorResponse {
  return vector(
    NODE_PODS.map((n, i) => ({
      metric: { pod: n.pod, instance: n.instance, job: "mcp-data-platform" },
      value: values[i] ?? 0,
    })),
  );
}

const MiB = 1024 * 1024;

// promInstantFor returns a canned instant-query response based on the
// shape of the PromQL query (which "by (label)" grouping or scalar
// aggregate it is).
export function promInstantFor(query: string): PromVectorResponse {
  // --- Per-node health (Go runtime / process / MCP gauges) ---
  // Matched first because these are specific metric names; order matters so
  // they are not shadowed by the apigateway grouping checks below. Each
  // returns one series per node; node 2 is given a short uptime to exercise
  // the "restarted" badge.
  if (query.includes("process_start_time_seconds")) {
    return nodeVector([37_842, 540, 36_120]); // uptime seconds; node 2 just restarted
  }
  if (query.includes("process_cpu_seconds_total")) {
    return nodeVector([0.041, 0.058, 0.033]); // cores
  }
  if (query.includes("process_resident_memory_bytes")) {
    return nodeVector([214 * MiB, 236 * MiB, 205 * MiB]);
  }
  if (query.includes("go_memstats_heap_alloc_bytes")) {
    return nodeVector([128 * MiB, 145 * MiB, 119 * MiB]);
  }
  if (query.includes("go_goroutines")) {
    return nodeVector([54, 61, 49]);
  }
  if (query.includes("mcp_inflight_tool_calls")) {
    return nodeVector([2, 3, 1]);
  }
  // connection x operation flow (sankey source). Checked before the
  // plain "by (connection)" branch since that string is not a substring of
  // "by (connection, operation_id)".
  if (query.includes("by (connection, operation_id)")) {
    return vector([
      { metric: { connection: "salesforce", operation_id: "listContacts" }, value: 8421 },
      { metric: { connection: "salesforce", operation_id: "getAccount" }, value: 5320 },
      { metric: { connection: "salesforce", operation_id: "createEvent" }, value: 2110 },
      { metric: { connection: "salesforce", operation_id: "updateLead" }, value: 1240 },
      { metric: { connection: "salesforce", operation_id: "listOpportunities" }, value: 1880 },
      { metric: { connection: "stripe", operation_id: "listCharges" }, value: 4810 },
      { metric: { connection: "stripe", operation_id: "getCustomer" }, value: 2900 },
      { metric: { connection: "stripe", operation_id: "createRefund" }, value: 640 },
      { metric: { connection: "stripe", operation_id: "listInvoices" }, value: 1530 },
      { metric: { connection: "stripe", operation_id: "getBalance" }, value: 720 },
      { metric: { connection: "github", operation_id: "listRepos" }, value: 3120 },
      { metric: { connection: "github", operation_id: "getIssue" }, value: 1870 },
      { metric: { connection: "github", operation_id: "createComment" }, value: 410 },
      { metric: { connection: "github", operation_id: "listPullRequests" }, value: 1340 },
      { metric: { connection: "shopify", operation_id: "listOrders" }, value: 6210 },
      { metric: { connection: "shopify", operation_id: "getProduct" }, value: 3480 },
      { metric: { connection: "shopify", operation_id: "updateInventory" }, value: 1990 },
      { metric: { connection: "shopify", operation_id: "listCustomers" }, value: 1120 },
      { metric: { connection: "zendesk", operation_id: "listTickets" }, value: 2760 },
      { metric: { connection: "zendesk", operation_id: "getTicket" }, value: 1810 },
      { metric: { connection: "zendesk", operation_id: "addComment" }, value: 530 },
      { metric: { connection: "hubspot", operation_id: "listDeals" }, value: 2240 },
      { metric: { connection: "hubspot", operation_id: "getCompany" }, value: 1390 },
      { metric: { connection: "api-test-fixture", operation_id: "whoami" }, value: 2050 },
      { metric: { connection: "api-test-fixture", operation_id: "lorem" }, value: 980 },
    ]);
  }
  // outbound calls grouped by status_category (success / client_error /
  // server_error) for the inbound-vs-outbound health split.
  if (query.includes("apigateway_outbound") && query.includes("by (status_category)")) {
    return vector([
      { metric: { status_category: "success" }, value: 26410 },
      { metric: { status_category: "client_error" }, value: 820 },
      { metric: { status_category: "server_error" }, value: 190 },
    ]);
  }
  if (query.includes("apigateway_outbound")) {
    return scalarVector(27420); // total outbound calls
  }
  if (query.includes("by (connection)")) {
    return vector([
      { metric: { connection: "salesforce" }, value: 18971 },
      { metric: { connection: "shopify" }, value: 12800 },
      { metric: { connection: "stripe" }, value: 10600 },
      { metric: { connection: "github" }, value: 6740 },
      { metric: { connection: "zendesk" }, value: 5100 },
      { metric: { connection: "hubspot" }, value: 3630 },
      { metric: { connection: "api-test-fixture" }, value: 4096 },
    ]);
  }
  if (query.includes("by (operation_id)")) {
    return vector([
      { metric: { operation_id: "listContacts" }, value: 8421 },
      { metric: { operation_id: "listOrders" }, value: 6210 },
      { metric: { operation_id: "getAccount" }, value: 5320 },
      { metric: { operation_id: "listCharges" }, value: 4810 },
      { metric: { operation_id: "getProduct" }, value: 3480 },
      { metric: { operation_id: "listRepos" }, value: 3120 },
      { metric: { operation_id: "getCustomer" }, value: 2900 },
      { metric: { operation_id: "listTickets" }, value: 2760 },
      { metric: { operation_id: "listDeals" }, value: 2240 },
      { metric: { operation_id: "createEvent" }, value: 2110 },
    ]);
  }
  if (query.includes("by (status_class)")) {
    return vector([
      { metric: { status_class: "2xx" }, value: 7600 },
      { metric: { status_class: "4xx" }, value: 420 },
      { metric: { status_class: "5xx" }, value: 103 },
    ]);
  }
  if (query.includes("by (method)")) {
    return vector([
      { metric: { method: "GET" }, value: 6900 },
      { metric: { method: "POST" }, value: 1223 },
    ]);
  }
  if (query.includes("by (identity)")) {
    return vector([
      { metric: { identity: "nifi-etl" }, value: 7800 },
      { metric: { identity: "analytics-cron" }, value: 323 },
    ]);
  }
  if (query.includes("histogram_quantile")) {
    if (query.includes("0.99")) return scalarVector(1.42);
    if (query.includes("0.95")) return scalarVector(0.88);
    return scalarVector(0.142);
  }
  if (query.includes("status_class=~")) {
    // error-rate numerator/full expression -> a fraction
    return scalarVector(0.031);
  }
  // total requests (sum(increase(...)))
  return scalarVector(18432);
}

// promRangeFor returns a canned range-query (matrix) response. For a
// `by (status_class)` grouping it returns three series (2xx/4xx/5xx) with
// realistic relative magnitudes so a stacked area reads as healthy-with-a-
// little-error; otherwise a single aggregated wave for the request-rate
// line. The query is inspected so the shape matches what the caller asked.
export function promRangeFor(
  query: string,
  start: number,
  end: number,
  step: number,
): PromMatrixResponse {
  const safeStep = step > 0 ? step : 60;
  const wave = (base: number, amp: number, phase: number): [number, string][] => {
    const out: [number, string][] = [];
    for (let t = start; t <= end; t += safeStep) {
      const k = (t - start) / safeStep;
      const v = Math.max(0, base + amp * Math.sin(k / 3 + phase));
      out.push([t, v.toFixed(3)]);
    }
    return out;
  };

  if (query.includes("by (status_class)")) {
    return {
      status: "success",
      data: {
        resultType: "matrix",
        result: [
          { metric: { status_class: "2xx" }, values: wave(34, 14, 0) },
          { metric: { status_class: "4xx" }, values: wave(4, 2.5, 1) },
          { metric: { status_class: "5xx" }, values: wave(1, 0.8, 2) },
        ],
      },
    };
  }

  // Per-node CPU / memory trend: one matrix series per node, each a wave
  // around that node's baseline so the Health charts differ per node.
  const perNode = (bases: number[], amp: number) => ({
    status: "success" as const,
    data: {
      resultType: "matrix" as const,
      result: NODE_PODS.map((n, i) => ({
        metric: { pod: n.pod, instance: n.instance, job: "mcp-data-platform" },
        values: wave(bases[i] ?? 0, amp, i),
      })),
    },
  });
  if (query.includes("process_cpu_seconds_total")) {
    return perNode([0.041, 0.058, 0.033], 0.018);
  }
  if (query.includes("process_resident_memory_bytes")) {
    return perNode([214 * MiB, 236 * MiB, 205 * MiB], 12 * MiB);
  }

  return {
    status: "success",
    data: { resultType: "matrix", result: [{ metric: {}, values: wave(20, 15, 0) }] },
  };
}
