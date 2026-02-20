import type {
  AuditEvent,
  AuditSortColumn,
  BreakdownEntry,
  Insight,
  InsightStats,
  Overview,
  PerformanceStats,
  TimeseriesBucket,
} from "@/api/types";
import { http, HttpResponse } from "msw";
import { mockAuditEvents } from "./data/audit";
import { mockInsights, mockChangesets } from "./data/knowledge";
import { mockPersonas, mockPersonaDetails } from "./data/personas";
import { mockSystemInfo, mockTools, mockConnections } from "./data/system";
import { mockToolSchemas, generateMockResult } from "./data/tools";

const BASE = "/api/v1/admin";

// ---------------------------------------------------------------------------
// Helpers — compute metrics from filtered events
// ---------------------------------------------------------------------------

function filterByTimeRange(url: URL, events: AuditEvent[]): AuditEvent[] {
  const startTime = url.searchParams.get("start_time");
  const endTime = url.searchParams.get("end_time");
  let filtered = events;
  if (startTime) filtered = filtered.filter((e) => e.timestamp >= startTime);
  if (endTime) filtered = filtered.filter((e) => e.timestamp <= endTime);
  return filtered;
}

function avg(nums: number[]): number {
  if (nums.length === 0) return 0;
  return nums.reduce((s, n) => s + n, 0) / nums.length;
}

function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0;
  const idx = Math.ceil((p / 100) * sorted.length) - 1;
  return sorted[Math.max(0, idx)]!;
}

function computeOverview(events: AuditEvent[]): Overview {
  const total = events.length;
  const successes = events.filter((e) => e.success).length;
  const enriched = events.filter((e) => e.enrichment_applied).length;
  return {
    total_calls: total,
    success_rate: total > 0 ? successes / total : 0,
    avg_duration_ms: avg(events.map((e) => e.duration_ms)),
    unique_users: new Set(events.map((e) => e.user_id)).size,
    unique_tools: new Set(events.map((e) => e.tool_name)).size,
    enrichment_rate: total > 0 ? enriched / total : 0,
    error_count: total - successes,
  };
}

function computeBreakdown(
  events: AuditEvent[],
  groupBy: string,
  limit: number,
): BreakdownEntry[] {
  const groups = new Map<string, AuditEvent[]>();
  for (const e of events) {
    const key = (e[groupBy as keyof AuditEvent] as string) ?? "unknown";
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key)!.push(e);
  }
  return [...groups.entries()]
    .map(([dim, evts]) => ({
      dimension: dim,
      count: evts.length,
      success_rate: evts.filter((e) => e.success).length / evts.length,
      avg_duration_ms: avg(evts.map((e) => e.duration_ms)),
    }))
    .sort((a, b) => b.count - a.count)
    .slice(0, limit);
}

function computePerformance(events: AuditEvent[]): PerformanceStats {
  const durations = events.map((e) => e.duration_ms).sort((a, b) => a - b);
  return {
    p50_ms: percentile(durations, 50),
    p95_ms: percentile(durations, 95),
    p99_ms: percentile(durations, 99),
    avg_ms: avg(durations),
    max_ms: durations.length > 0 ? durations[durations.length - 1]! : 0,
    avg_response_chars: avg(events.map((e) => e.response_chars)),
    avg_request_chars: avg(events.map((e) => e.request_chars)),
  };
}

function computeTimeseries(
  events: AuditEvent[],
  startTime: string,
  endTime: string,
  resolution: string,
): TimeseriesBucket[] {
  const start = new Date(startTime).getTime();
  const end = new Date(endTime).getTime();
  let bucketMs: number;
  switch (resolution) {
    case "minute":
      bucketMs = 60_000;
      break;
    case "day":
      bucketMs = 86_400_000;
      break;
    default:
      bucketMs = 3_600_000;
      break; // hour
  }

  // Index events by bucket for O(n) instead of O(n*b)
  const bucketMap = new Map<number, AuditEvent[]>();
  for (const e of events) {
    const et = new Date(e.timestamp).getTime();
    if (et < start || et >= end) continue;
    const key = Math.floor((et - start) / bucketMs);
    if (!bucketMap.has(key)) bucketMap.set(key, []);
    bucketMap.get(key)!.push(e);
  }

  const totalBuckets = Math.ceil((end - start) / bucketMs);
  const buckets: TimeseriesBucket[] = [];
  for (let i = 0; i < totalBuckets; i++) {
    const inBucket = bucketMap.get(i) ?? [];
    const successes = inBucket.filter((e) => e.success).length;
    buckets.push({
      bucket: new Date(start + i * bucketMs).toISOString(),
      count: inBucket.length,
      success_count: successes,
      error_count: inBucket.length - successes,
      avg_duration_ms: avg(inBucket.map((e) => e.duration_ms)),
    });
  }
  return buckets;
}

// ---------------------------------------------------------------------------
// Knowledge helpers
// ---------------------------------------------------------------------------

function computeInsightStats(insights: Insight[]): InsightStats {
  const byStatus: Record<string, number> = {};
  const byCategory: Record<string, number> = {};
  const byConfidence: Record<string, number> = {};
  const entityMap = new Map<
    string,
    { count: number; categories: Set<string>; latest: string }
  >();

  for (const ins of insights) {
    byStatus[ins.status] = (byStatus[ins.status] ?? 0) + 1;
    byCategory[ins.category] = (byCategory[ins.category] ?? 0) + 1;
    byConfidence[ins.confidence] = (byConfidence[ins.confidence] ?? 0) + 1;
    for (const urn of ins.entity_urns) {
      const existing = entityMap.get(urn);
      if (existing) {
        existing.count++;
        existing.categories.add(ins.category);
        if (ins.created_at > existing.latest) existing.latest = ins.created_at;
      } else {
        entityMap.set(urn, {
          count: 1,
          categories: new Set([ins.category]),
          latest: ins.created_at,
        });
      }
    }
  }

  return {
    total_pending: byStatus["pending"] ?? 0,
    by_entity: [...entityMap.entries()]
      .map(([urn, v]) => ({
        entity_urn: urn,
        count: v.count,
        categories: [...v.categories],
        latest_at: v.latest,
      }))
      .sort((a, b) => b.count - a.count),
    by_category: byCategory,
    by_confidence: byConfidence,
    by_status: byStatus,
  };
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

export const handlers = [
  // Public (unauthenticated)
  http.get(`${BASE}/public/branding`, () =>
    HttpResponse.json({
      name: mockSystemInfo.name,
      portal_title: mockSystemInfo.portal_title,
      portal_logo: mockSystemInfo.portal_logo,
      portal_logo_light: mockSystemInfo.portal_logo_light,
      portal_logo_dark: mockSystemInfo.portal_logo_dark,
    }),
  ),

  // System
  http.get(`${BASE}/system/info`, () => HttpResponse.json(mockSystemInfo)),

  http.get(`${BASE}/tools`, () =>
    HttpResponse.json({ tools: mockTools, total: mockTools.length }),
  ),

  http.get(`${BASE}/connections`, () =>
    HttpResponse.json({
      connections: mockConnections,
      total: mockConnections.length,
    }),
  ),

  // Audit event filters (unique users/tools for dropdown population)
  http.get(`${BASE}/audit/events/filters`, () => {
    const users = [...new Set(mockAuditEvents.map((e) => e.user_id))].sort();
    const tools = [...new Set(mockAuditEvents.map((e) => e.tool_name))].sort();
    return HttpResponse.json({ users, tools });
  }),

  // Audit events
  http.get(`${BASE}/audit/events`, ({ request }) => {
    const url = new URL(request.url);
    const page = parseInt(url.searchParams.get("page") ?? "1", 10);
    const perPage = parseInt(url.searchParams.get("per_page") ?? "20", 10);
    const userId = url.searchParams.get("user_id");
    const toolName = url.searchParams.get("tool_name");
    const success = url.searchParams.get("success");
    const search = url.searchParams.get("search")?.toLowerCase();
    const sortBy = url.searchParams.get("sort_by") as AuditSortColumn | null;
    const sortOrder = url.searchParams.get("sort_order") ?? "desc";

    let filtered = filterByTimeRange(url, mockAuditEvents);
    if (userId) filtered = filtered.filter((e) => e.user_id === userId);
    if (toolName) filtered = filtered.filter((e) => e.tool_name === toolName);
    if (success !== null && success !== undefined && success !== "")
      filtered = filtered.filter((e) => String(e.success) === success);
    if (search) {
      filtered = filtered.filter(
        (e) =>
          e.user_id.toLowerCase().includes(search) ||
          e.tool_name.toLowerCase().includes(search) ||
          (e.toolkit_kind ?? "").toLowerCase().includes(search) ||
          (e.connection ?? "").toLowerCase().includes(search) ||
          (e.persona ?? "").toLowerCase().includes(search) ||
          (e.error_message ?? "").toLowerCase().includes(search) ||
          e.id.toLowerCase().includes(search),
      );
    }

    if (sortBy) {
      const dir = sortOrder === "asc" ? 1 : -1;
      filtered.sort((a, b) => {
        const av = a[sortBy as keyof AuditEvent];
        const bv = b[sortBy as keyof AuditEvent];
        if (av == null && bv == null) return 0;
        if (av == null) return dir;
        if (bv == null) return -dir;
        if (av < bv) return -dir;
        if (av > bv) return dir;
        return 0;
      });
    }

    const start = (page - 1) * perPage;
    const data = filtered.slice(start, start + perPage);

    return HttpResponse.json({
      data,
      total: filtered.length,
      page,
      per_page: perPage,
    });
  }),

  // Audit metrics — all computed dynamically from filtered events
  http.get(`${BASE}/audit/metrics/timeseries`, ({ request }) => {
    const url = new URL(request.url);
    const filtered = filterByTimeRange(url, mockAuditEvents);
    const resolution = url.searchParams.get("resolution") ?? "hour";
    const startTime = url.searchParams.get("start_time");
    const endTime = url.searchParams.get("end_time");
    if (!startTime || !endTime) return HttpResponse.json([]);
    return HttpResponse.json(
      computeTimeseries(filtered, startTime, endTime, resolution),
    );
  }),

  http.get(`${BASE}/audit/metrics/breakdown`, ({ request }) => {
    const url = new URL(request.url);
    const filtered = filterByTimeRange(url, mockAuditEvents);
    const groupBy = url.searchParams.get("group_by") ?? "tool_name";
    const limit = parseInt(url.searchParams.get("limit") ?? "10", 10);
    return HttpResponse.json(computeBreakdown(filtered, groupBy, limit));
  }),

  http.get(`${BASE}/audit/metrics/overview`, ({ request }) => {
    const url = new URL(request.url);
    const filtered = filterByTimeRange(url, mockAuditEvents);
    return HttpResponse.json(computeOverview(filtered));
  }),

  http.get(`${BASE}/audit/metrics/performance`, ({ request }) => {
    const url = new URL(request.url);
    const filtered = filterByTimeRange(url, mockAuditEvents);
    return HttpResponse.json(computePerformance(filtered));
  }),

  // -------------------------------------------------------------------------
  // Knowledge — Insights
  // -------------------------------------------------------------------------

  // Stats must come before the parameterized :id route
  http.get(`${BASE}/knowledge/insights/stats`, () => {
    return HttpResponse.json(computeInsightStats(mockInsights));
  }),

  http.get(`${BASE}/knowledge/insights`, ({ request }) => {
    const url = new URL(request.url);
    const page = parseInt(url.searchParams.get("page") ?? "1", 10);
    const perPage = parseInt(url.searchParams.get("per_page") ?? "20", 10);
    const status = url.searchParams.get("status");
    const category = url.searchParams.get("category");
    const confidence = url.searchParams.get("confidence");
    const entityUrn = url.searchParams.get("entity_urn");
    const capturedBy = url.searchParams.get("captured_by");

    let filtered = [...mockInsights];
    if (status) filtered = filtered.filter((i) => i.status === status);
    if (category) filtered = filtered.filter((i) => i.category === category);
    if (confidence)
      filtered = filtered.filter((i) => i.confidence === confidence);
    if (entityUrn)
      filtered = filtered.filter((i) => i.entity_urns.includes(entityUrn));
    if (capturedBy)
      filtered = filtered.filter((i) => i.captured_by === capturedBy);

    const start = (page - 1) * perPage;
    const data = filtered.slice(start, start + perPage);

    return HttpResponse.json({
      data,
      total: filtered.length,
      page,
      per_page: perPage,
    });
  }),

  http.get(`${BASE}/knowledge/insights/:id`, ({ params }) => {
    const insight = mockInsights.find((i) => i.id === params["id"]);
    if (!insight) return new HttpResponse(null, { status: 404 });
    return HttpResponse.json(insight);
  }),

  http.put(`${BASE}/knowledge/insights/:id/status`, async ({ params, request }) => {
    const insight = mockInsights.find((i) => i.id === params["id"]);
    if (!insight) return new HttpResponse(null, { status: 404 });

    const body = (await request.json()) as {
      status: string;
      review_notes?: string;
    };
    insight.status = body.status;
    insight.reviewed_by = "admin@acme-corp.com";
    insight.reviewed_at = new Date().toISOString();
    if (body.review_notes) insight.review_notes = body.review_notes;

    return HttpResponse.json(insight);
  }),

  // -------------------------------------------------------------------------
  // Knowledge — Changesets
  // -------------------------------------------------------------------------

  http.get(`${BASE}/knowledge/changesets`, ({ request }) => {
    const url = new URL(request.url);
    const page = parseInt(url.searchParams.get("page") ?? "1", 10);
    const perPage = parseInt(url.searchParams.get("per_page") ?? "20", 10);
    const entityUrn = url.searchParams.get("entity_urn");
    const appliedBy = url.searchParams.get("applied_by");
    const rolledBack = url.searchParams.get("rolled_back");

    let filtered = [...mockChangesets];
    if (entityUrn)
      filtered = filtered.filter((c) => c.target_urn.includes(entityUrn));
    if (appliedBy)
      filtered = filtered.filter((c) => c.applied_by === appliedBy);
    if (rolledBack === "true")
      filtered = filtered.filter((c) => c.rolled_back);
    if (rolledBack === "false")
      filtered = filtered.filter((c) => !c.rolled_back);

    const start = (page - 1) * perPage;
    const data = filtered.slice(start, start + perPage);

    return HttpResponse.json({
      data,
      total: filtered.length,
      page,
      per_page: perPage,
    });
  }),

  http.get(`${BASE}/knowledge/changesets/:id`, ({ params }) => {
    const changeset = mockChangesets.find((c) => c.id === params["id"]);
    if (!changeset) return new HttpResponse(null, { status: 404 });
    return HttpResponse.json(changeset);
  }),

  http.post(`${BASE}/knowledge/changesets/:id/rollback`, ({ params }) => {
    const changeset = mockChangesets.find((c) => c.id === params["id"]);
    if (!changeset) return new HttpResponse(null, { status: 404 });

    changeset.rolled_back = true;
    changeset.rolled_back_by = "admin@acme-corp.com";
    changeset.rolled_back_at = new Date().toISOString();

    return HttpResponse.json(changeset);
  }),

  // -------------------------------------------------------------------------
  // Personas
  // -------------------------------------------------------------------------

  http.get(`${BASE}/personas`, () => {
    return HttpResponse.json({
      personas: mockPersonas,
      total: mockPersonas.length,
    });
  }),

  http.get(`${BASE}/personas/:name`, ({ params }) => {
    const detail = mockPersonaDetails[params["name"] as string];
    if (!detail) return new HttpResponse(null, { status: 404 });
    return HttpResponse.json(detail);
  }),

  http.post(`${BASE}/personas`, async ({ request }) => {
    const body = (await request.json()) as {
      name: string;
      display_name: string;
      description?: string;
      roles: string[];
      allow_tools: string[];
      deny_tools?: string[];
      priority?: number;
    };

    if (!body.name || !body.display_name) {
      return HttpResponse.json(
        { detail: "name and display_name are required" },
        { status: 400 },
      );
    }

    if (mockPersonaDetails[body.name]) {
      return HttpResponse.json(
        { detail: "persona already exists" },
        { status: 409 },
      );
    }

    const detail = {
      name: body.name,
      display_name: body.display_name,
      description: body.description,
      roles: body.roles ?? [],
      priority: body.priority ?? 0,
      allow_tools: body.allow_tools ?? [],
      deny_tools: body.deny_tools ?? [],
      tools: [] as string[],
    };

    mockPersonaDetails[body.name] = detail;
    mockPersonas.push({
      name: detail.name,
      display_name: detail.display_name,
      roles: detail.roles,
      tool_count: 0,
    });

    return HttpResponse.json(detail, { status: 201 });
  }),

  http.put(`${BASE}/personas/:name`, async ({ params, request }) => {
    const name = params["name"] as string;
    const existing = mockPersonaDetails[name];
    if (!existing) return new HttpResponse(null, { status: 404 });

    const body = (await request.json()) as {
      display_name: string;
      description?: string;
      roles?: string[];
      allow_tools?: string[];
      deny_tools?: string[];
      priority?: number;
    };

    if (!body.display_name) {
      return HttpResponse.json(
        { detail: "display_name is required" },
        { status: 400 },
      );
    }

    existing.display_name = body.display_name;
    if (body.description !== undefined) existing.description = body.description;
    if (body.roles) existing.roles = body.roles;
    if (body.allow_tools) existing.allow_tools = body.allow_tools;
    if (body.deny_tools) existing.deny_tools = body.deny_tools;
    if (body.priority !== undefined) existing.priority = body.priority;

    // Update summary
    const idx = mockPersonas.findIndex((p) => p.name === name);
    if (idx !== -1) {
      mockPersonas[idx]!.display_name = existing.display_name;
      mockPersonas[idx]!.roles = existing.roles;
    }

    return HttpResponse.json(existing);
  }),

  http.delete(`${BASE}/personas/:name`, ({ params }) => {
    const name = params["name"] as string;

    if (name === "admin") {
      return HttpResponse.json(
        { detail: "cannot delete the admin persona" },
        { status: 409 },
      );
    }

    if (!mockPersonaDetails[name]) {
      return new HttpResponse(null, { status: 404 });
    }

    delete mockPersonaDetails[name];
    const idx = mockPersonas.findIndex((p) => p.name === name);
    if (idx !== -1) mockPersonas.splice(idx, 1);

    return HttpResponse.json({ status: "deleted" });
  }),

  // -------------------------------------------------------------------------
  // Tools — Schema & Execution
  // -------------------------------------------------------------------------

  http.get(`${BASE}/tools/schemas`, () => {
    return HttpResponse.json({ schemas: mockToolSchemas });
  }),

  http.post(`${BASE}/tools/call`, async ({ request }) => {
    const body = (await request.json()) as {
      tool_name: string;
      connection: string;
      parameters: Record<string, unknown>;
    };

    const result = generateMockResult(body.tool_name, body.parameters);

    // Simulate variable latency
    await new Promise((resolve) =>
      setTimeout(resolve, 200 + Math.random() * 600),
    );

    return HttpResponse.json(result);
  }),

];
