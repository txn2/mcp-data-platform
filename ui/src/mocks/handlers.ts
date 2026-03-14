import type {
  AuditEvent,
  AuditSortColumn,
  BreakdownEntry,
  Insight,
  InsightStats,
  Overview,
  PerformanceStats,
  TimeseriesBucket,
} from "@/api/admin/types";
import type { Share } from "@/api/portal/types";
import { http, HttpResponse } from "msw";
import { mockAuditEvents } from "./data/audit";
import { mockInsights, mockChangesets } from "./data/knowledge";
import { mockPersonas, mockPersonaDetails } from "./data/personas";
import { mockSystemInfo, mockTools, mockConnections } from "./data/system";
import { mockToolSchemas, generateMockResult } from "./data/tools";
import { mockAssets, mockShares, mockSharedWithMe } from "./data/assets";
import { mockContent } from "./data/content";

const ADMIN_BASE = "/api/v1/admin";
const PORTAL_BASE = "/api/v1/portal";

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
      break;
  }

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
// Portal helpers
// ---------------------------------------------------------------------------

const portalAssets = [...mockAssets];
const portalShares: Record<string, Share[]> = JSON.parse(
  JSON.stringify(mockShares),
);
let shareCounter = 100;

function parseDuration(s: string): number {
  const match = s.match(/^(\d+)(h|m|s)$/);
  if (!match) return 24 * 60 * 60 * 1000;
  const [, val, unit] = match;
  const n = parseInt(val!, 10);
  switch (unit) {
    case "h":
      return n * 60 * 60 * 1000;
    case "m":
      return n * 60 * 1000;
    case "s":
      return n * 1000;
    default:
      return 24 * 60 * 60 * 1000;
  }
}

// ---------------------------------------------------------------------------
// Handlers — combined admin + portal
// ---------------------------------------------------------------------------

export const handlers = [
  // =========================================================================
  // Public (unauthenticated)
  // =========================================================================

  http.get(`${ADMIN_BASE}/public/branding`, () =>
    HttpResponse.json({
      name: mockSystemInfo.name,
      portal_title: mockSystemInfo.portal_title,
      portal_logo: mockSystemInfo.portal_logo,
      portal_logo_light: mockSystemInfo.portal_logo_light,
      portal_logo_dark: mockSystemInfo.portal_logo_dark,
      oidc_enabled: false,
    }),
  ),

  // =========================================================================
  // Portal — /me (mock: return admin user)
  // =========================================================================

  http.get(`${PORTAL_BASE}/me`, () =>
    HttpResponse.json({
      user_id: "sarah.chen@acme-corp.com",
      email: "sarah.chen@acme-corp.com",
      roles: ["admin"],
      is_admin: true,
      persona: "admin",
      tools: [
        "trino_query",
        "trino_describe_table",
        "trino_browse",
        "trino_explain",
        "trino_execute",
        "datahub_search",
        "datahub_get_entity",
        "datahub_get_schema",
        "datahub_get_lineage",
        "datahub_browse",
        "s3_list_objects",
        "s3_get_object",
        "s3_list_buckets",
        "capture_insight",
        "apply_knowledge",
        "save_artifact",
        "manage_artifact",
      ],
    }),
  ),

  // =========================================================================
  // Admin API
  // =========================================================================

  http.get(`${ADMIN_BASE}/system/info`, () => HttpResponse.json(mockSystemInfo)),

  http.get(`${ADMIN_BASE}/tools`, () =>
    HttpResponse.json({ tools: mockTools, total: mockTools.length }),
  ),

  http.get(`${ADMIN_BASE}/connections`, () =>
    HttpResponse.json({
      connections: mockConnections,
      total: mockConnections.length,
    }),
  ),

  http.get(`${ADMIN_BASE}/audit/events/filters`, () => {
    const users = [...new Set(mockAuditEvents.map((e) => e.user_id))].sort();
    const tools = [...new Set(mockAuditEvents.map((e) => e.tool_name))].sort();
    return HttpResponse.json({ users, tools });
  }),

  http.get(`${ADMIN_BASE}/audit/events`, ({ request }) => {
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

  http.get(`${ADMIN_BASE}/audit/metrics/timeseries`, ({ request }) => {
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

  http.get(`${ADMIN_BASE}/audit/metrics/breakdown`, ({ request }) => {
    const url = new URL(request.url);
    const filtered = filterByTimeRange(url, mockAuditEvents);
    const groupBy = url.searchParams.get("group_by") ?? "tool_name";
    const limit = parseInt(url.searchParams.get("limit") ?? "10", 10);
    return HttpResponse.json(computeBreakdown(filtered, groupBy, limit));
  }),

  http.get(`${ADMIN_BASE}/audit/metrics/overview`, ({ request }) => {
    const url = new URL(request.url);
    const filtered = filterByTimeRange(url, mockAuditEvents);
    return HttpResponse.json(computeOverview(filtered));
  }),

  http.get(`${ADMIN_BASE}/audit/metrics/performance`, ({ request }) => {
    const url = new URL(request.url);
    const filtered = filterByTimeRange(url, mockAuditEvents);
    return HttpResponse.json(computePerformance(filtered));
  }),

  http.get(`${ADMIN_BASE}/knowledge/insights/stats`, () => {
    return HttpResponse.json(computeInsightStats(mockInsights));
  }),

  http.get(`${ADMIN_BASE}/knowledge/insights`, ({ request }) => {
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

  http.get(`${ADMIN_BASE}/knowledge/insights/:id`, ({ params }) => {
    const insight = mockInsights.find((i) => i.id === params["id"]);
    if (!insight) return new HttpResponse(null, { status: 404 });
    return HttpResponse.json(insight);
  }),

  http.put(`${ADMIN_BASE}/knowledge/insights/:id/status`, async ({ params, request }) => {
    const insight = mockInsights.find((i) => i.id === params["id"]);
    if (!insight) return new HttpResponse(null, { status: 404 });

    const body = (await request.json()) as {
      status: string;
      review_notes?: string;
    };
    insight.status = body.status;
    insight.reviewed_by = "admin@example.com";
    insight.reviewed_at = new Date().toISOString();
    if (body.review_notes) insight.review_notes = body.review_notes;

    return HttpResponse.json(insight);
  }),

  http.get(`${ADMIN_BASE}/knowledge/changesets`, ({ request }) => {
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

  http.get(`${ADMIN_BASE}/knowledge/changesets/:id`, ({ params }) => {
    const changeset = mockChangesets.find((c) => c.id === params["id"]);
    if (!changeset) return new HttpResponse(null, { status: 404 });
    return HttpResponse.json(changeset);
  }),

  http.post(`${ADMIN_BASE}/knowledge/changesets/:id/rollback`, ({ params }) => {
    const changeset = mockChangesets.find((c) => c.id === params["id"]);
    if (!changeset) return new HttpResponse(null, { status: 404 });

    changeset.rolled_back = true;
    changeset.rolled_back_by = "admin@example.com";
    changeset.rolled_back_at = new Date().toISOString();

    return HttpResponse.json(changeset);
  }),

  http.get(`${ADMIN_BASE}/personas`, () => {
    return HttpResponse.json({
      personas: mockPersonas,
      total: mockPersonas.length,
    });
  }),

  http.get(`${ADMIN_BASE}/personas/:name`, ({ params }) => {
    const detail = mockPersonaDetails[params["name"] as string];
    if (!detail) return new HttpResponse(null, { status: 404 });
    return HttpResponse.json(detail);
  }),

  http.post(`${ADMIN_BASE}/personas`, async ({ request }) => {
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

  http.put(`${ADMIN_BASE}/personas/:name`, async ({ params, request }) => {
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

    const idx = mockPersonas.findIndex((p) => p.name === name);
    if (idx !== -1) {
      mockPersonas[idx]!.display_name = existing.display_name;
      mockPersonas[idx]!.roles = existing.roles;
    }

    return HttpResponse.json(existing);
  }),

  http.delete(`${ADMIN_BASE}/personas/:name`, ({ params }) => {
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

  // =========================================================================
  // Admin — Assets
  // =========================================================================

  http.get(`${ADMIN_BASE}/assets`, ({ request }) => {
    const url = new URL(request.url);
    const search = url.searchParams.get("search")?.toLowerCase();
    const limit = parseInt(url.searchParams.get("limit") ?? "50", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    let filtered = portalAssets.filter((a) => !a.deleted_at);
    if (search) {
      filtered = filtered.filter(
        (a) =>
          a.name.toLowerCase().includes(search) ||
          a.description.toLowerCase().includes(search) ||
          a.owner_email.toLowerCase().includes(search) ||
          a.owner_id.toLowerCase().includes(search) ||
          a.tags.some((t: string) => t.toLowerCase().includes(search)),
      );
    }

    const page = filtered.slice(offset, offset + limit);
    return HttpResponse.json({
      data: page,
      total: filtered.length,
      limit,
      offset,
    });
  }),

  http.get(`${ADMIN_BASE}/assets/:id`, ({ params }) => {
    const asset = portalAssets.find(
      (a) => a.id === params.id && !a.deleted_at,
    );
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    return HttpResponse.json(asset);
  }),

  http.get(`${ADMIN_BASE}/assets/:id/content`, ({ params }) => {
    const id = params.id as string;
    const asset = portalAssets.find((a) => a.id === id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = mockContent[id] ?? `[Mock content for ${asset.name}]`;
    return new HttpResponse(body, {
      headers: { "Content-Type": asset.content_type },
    });
  }),

  http.put(`${ADMIN_BASE}/assets/:id/content`, async ({ params, request }) => {
    const id = params.id as string;
    const idx = portalAssets.findIndex((a) => a.id === id && !a.deleted_at);
    if (idx === -1) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = await request.text();
    mockContent[id] = body;
    portalAssets[idx]!.size_bytes = body.length;
    portalAssets[idx]!.updated_at = new Date().toISOString();
    return HttpResponse.json({ status: "updated" });
  }),

  http.put(`${ADMIN_BASE}/assets/:id`, async ({ params, request }) => {
    const idx = portalAssets.findIndex(
      (a) => a.id === params.id && !a.deleted_at,
    );
    if (idx === -1) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = (await request.json()) as Record<string, unknown>;
    if (body.name !== undefined) portalAssets[idx]!.name = body.name as string;
    if (body.description !== undefined)
      portalAssets[idx]!.description = body.description as string;
    if (body.tags !== undefined)
      portalAssets[idx]!.tags = body.tags as string[];
    portalAssets[idx]!.updated_at = new Date().toISOString();
    return HttpResponse.json(portalAssets[idx]);
  }),

  http.put(`${ADMIN_BASE}/assets/:id/thumbnail`, ({ params }) => {
    const asset = portalAssets.find((a) => a.id === params.id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    asset.thumbnail_s3_key = `thumbnails/${asset.id}.png`;
    return new HttpResponse(null, { status: 204 });
  }),

  http.delete(`${ADMIN_BASE}/assets/:id`, ({ params }) => {
    const idx = portalAssets.findIndex(
      (a) => a.id === params.id && !a.deleted_at,
    );
    if (idx === -1) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    portalAssets[idx]!.deleted_at = new Date().toISOString();
    return HttpResponse.json({ status: "deleted" });
  }),

  http.get(`${ADMIN_BASE}/tools/schemas`, () => {
    return HttpResponse.json({ schemas: mockToolSchemas });
  }),

  http.post(`${ADMIN_BASE}/tools/call`, async ({ request }) => {
    const body = (await request.json()) as {
      tool_name: string;
      connection: string;
      parameters: Record<string, unknown>;
    };

    const result = generateMockResult(body.tool_name, body.parameters);

    await new Promise((resolve) =>
      setTimeout(resolve, 200 + Math.random() * 600),
    );

    return HttpResponse.json(result);
  }),

  // =========================================================================
  // Portal API
  // =========================================================================

  http.get(`${PORTAL_BASE}/assets`, ({ request }) => {
    const url = new URL(request.url);
    const contentType = url.searchParams.get("content_type");
    const tag = url.searchParams.get("tag");
    const limit = parseInt(url.searchParams.get("limit") ?? "50", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    let filtered = portalAssets.filter((a) => !a.deleted_at);
    if (contentType) {
      filtered = filtered.filter((a) => a.content_type === contentType);
    }
    if (tag) {
      filtered = filtered.filter((a) =>
        a.tags.some((t: string) => t.toLowerCase().includes(tag.toLowerCase())),
      );
    }

    const page = filtered.slice(offset, offset + limit);

    // Build share summaries for the returned assets
    const share_summaries: Record<string, { has_user_share: boolean; has_public_link: boolean }> = {};
    for (const asset of page) {
      const shares = portalShares[asset.id];
      if (shares && shares.length > 0) {
        const active = shares.filter((s) => !s.revoked);
        share_summaries[asset.id] = {
          has_user_share: active.some((s) => !!s.shared_with_user_id),
          has_public_link: active.some((s) => !s.shared_with_user_id),
        };
      }
    }

    return HttpResponse.json({
      data: page,
      total: filtered.length,
      limit,
      offset,
      share_summaries,
    });
  }),

  http.get(`${PORTAL_BASE}/assets/:id`, ({ params }) => {
    const asset = portalAssets.find(
      (a) => a.id === params.id && !a.deleted_at,
    );
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    return HttpResponse.json(asset);
  }),

  http.get(`${PORTAL_BASE}/assets/:id/content`, ({ params }) => {
    const id = params.id as string;
    const asset = portalAssets.find((a) => a.id === id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = mockContent[id] ?? `[Mock content for ${asset.name}]`;
    return new HttpResponse(body, {
      headers: { "Content-Type": asset.content_type },
    });
  }),

  http.put(`${PORTAL_BASE}/assets/:id/content`, async ({ params, request }) => {
    const id = params.id as string;
    const idx = portalAssets.findIndex((a) => a.id === id && !a.deleted_at);
    if (idx === -1) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = await request.text();
    mockContent[id] = body;
    portalAssets[idx]!.size_bytes = body.length;
    portalAssets[idx]!.updated_at = new Date().toISOString();
    return HttpResponse.json({ status: "updated" });
  }),

  http.put(`${PORTAL_BASE}/assets/:id`, async ({ params, request }) => {
    const idx = portalAssets.findIndex(
      (a) => a.id === params.id && !a.deleted_at,
    );
    if (idx === -1) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = (await request.json()) as Record<string, unknown>;
    if (body.name !== undefined) portalAssets[idx]!.name = body.name as string;
    if (body.description !== undefined)
      portalAssets[idx]!.description = body.description as string;
    if (body.tags !== undefined)
      portalAssets[idx]!.tags = body.tags as string[];
    portalAssets[idx]!.updated_at = new Date().toISOString();
    return HttpResponse.json(portalAssets[idx]);
  }),

  http.put(`${PORTAL_BASE}/assets/:id/thumbnail`, ({ params }) => {
    const asset = portalAssets.find((a) => a.id === params.id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    asset.thumbnail_s3_key = `thumbnails/${asset.id}.png`;
    return new HttpResponse(null, { status: 204 });
  }),

  http.delete(`${PORTAL_BASE}/assets/:id`, ({ params }) => {
    const idx = portalAssets.findIndex(
      (a) => a.id === params.id && !a.deleted_at,
    );
    if (idx === -1) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    portalAssets[idx]!.deleted_at = new Date().toISOString();
    return new HttpResponse(null, { status: 204 });
  }),

  http.get(`${PORTAL_BASE}/assets/:assetId/shares`, ({ params }) => {
    const assetId = params.assetId as string;
    return HttpResponse.json(portalShares[assetId] ?? []);
  }),

  http.post(
    `${PORTAL_BASE}/assets/:assetId/shares`,
    async ({ params, request }) => {
      const assetId = params.assetId as string;
      const body = (await request.json()) as Record<string, unknown>;

      shareCounter++;
      const token = `tok_mock_${shareCounter}_${Math.random().toString(36).slice(2, 10)}`;

      const share: Share = {
        id: `shr-mock-${shareCounter}`,
        asset_id: assetId,
        token,
        created_by: "user-alice",
        shared_with_user_id: body.shared_with_user_id as string | undefined,
        permission: (body.permission as Share["permission"]) ?? "viewer",
        expires_at: body.expires_in
          ? new Date(
              Date.now() + parseDuration(body.expires_in as string),
            ).toISOString()
          : undefined,
        revoked: false,
        access_count: 0,
        created_at: new Date().toISOString(),
        hide_expiration: body.hide_expiration === true,
        notice_text: typeof body.notice_text === "string" ? body.notice_text : undefined,
      };

      if (!portalShares[assetId]) portalShares[assetId] = [];
      portalShares[assetId]!.push(share);

      return HttpResponse.json({
        share,
        share_url: `${window.location.origin}/portal/view/${token}`,
      });
    },
  ),

  http.delete(`${PORTAL_BASE}/shares/:id`, ({ params }) => {
    for (const list of Object.values(portalShares)) {
      const share = list.find((s) => s.id === params.id);
      if (share) {
        share.revoked = true;
        return new HttpResponse(null, { status: 204 });
      }
    }
    return HttpResponse.json({ detail: "Not found" }, { status: 404 });
  }),

  http.get(`${PORTAL_BASE}/shared-with-me`, ({ request }) => {
    const url = new URL(request.url);
    const limit = parseInt(url.searchParams.get("limit") ?? "50", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    const page = mockSharedWithMe.slice(offset, offset + limit);
    return HttpResponse.json({
      data: page,
      total: mockSharedWithMe.length,
      limit,
      offset,
    });
  }),

  // =========================================================================
  // Portal — Activity (user-scoped audit metrics)
  // =========================================================================

  http.get(`${PORTAL_BASE}/activity/overview`, ({ request }) => {
    const url = new URL(request.url);
    const userEvents = filterByTimeRange(
      url,
      mockAuditEvents.filter((e) => e.user_id === "sarah.chen@acme-corp.com"),
    );
    return HttpResponse.json(computeOverview(userEvents));
  }),

  http.get(`${PORTAL_BASE}/activity/timeseries`, ({ request }) => {
    const url = new URL(request.url);
    const userEvents = filterByTimeRange(
      url,
      mockAuditEvents.filter((e) => e.user_id === "sarah.chen@acme-corp.com"),
    );
    const resolution = url.searchParams.get("resolution") ?? "hour";
    const startTime = url.searchParams.get("start_time");
    const endTime = url.searchParams.get("end_time");
    if (!startTime || !endTime) return HttpResponse.json([]);
    return HttpResponse.json(
      computeTimeseries(userEvents, startTime, endTime, resolution),
    );
  }),

  http.get(`${PORTAL_BASE}/activity/breakdown`, ({ request }) => {
    const url = new URL(request.url);
    const userEvents = filterByTimeRange(
      url,
      mockAuditEvents.filter((e) => e.user_id === "sarah.chen@acme-corp.com"),
    );
    const groupBy = url.searchParams.get("group_by") ?? "tool_name";
    const limit = parseInt(url.searchParams.get("limit") ?? "10", 10);
    return HttpResponse.json(computeBreakdown(userEvents, groupBy, limit));
  }),

  // =========================================================================
  // Portal — Knowledge (user-scoped insights)
  // =========================================================================

  http.get(`${PORTAL_BASE}/knowledge/insights/stats`, () => {
    const userInsights = mockInsights.filter(
      (i) => i.captured_by === "sarah.chen@acme-corp.com",
    );
    return HttpResponse.json(computeInsightStats(userInsights));
  }),

  http.get(`${PORTAL_BASE}/knowledge/insights`, ({ request }) => {
    const url = new URL(request.url);
    const status = url.searchParams.get("status");
    const category = url.searchParams.get("category");
    const limit = parseInt(url.searchParams.get("limit") ?? "20", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    let filtered = mockInsights.filter(
      (i) => i.captured_by === "sarah.chen@acme-corp.com",
    );
    if (status) filtered = filtered.filter((i) => i.status === status);
    if (category) filtered = filtered.filter((i) => i.category === category);

    const data = filtered.slice(offset, offset + limit);
    return HttpResponse.json({
      data,
      total: filtered.length,
      limit,
      offset,
    });
  }),
];
