import type {
  APIRouteRule,
  AuditEvent,
  AuditSortColumn,
  BreakdownEntry,
  Insight,
  InsightStats,
  Overview,
  PerformanceStats,
  TimeseriesBucket,
  ToolDetail,
  ToolPersonaAccess,
} from "@/api/admin/types";
import type { Share } from "@/api/portal/types";
import { http, HttpResponse } from "msw";
import { agentSessions, mockAuditEvents } from "./data/audit";
import { mockInsights, mockChangesets } from "./data/knowledge";
import { mockAPIRouteConnections } from "./data/apis";
import { mockPersonas, mockPersonaDetails } from "./data/personas";
import { mockSystemInfo, mockTools, mockConnections } from "./data/system";
import { mockToolSchemas, generateMockResult } from "./data/tools";
import { mockEnrichmentRules } from "./data/enrichment";
import { mockAssets, mockShares, mockSharedWithMe } from "./data/assets";
import { versionsForAsset } from "./data/assetVersions";
import {
  isThemeable,
  isThumbnailSupported,
  thumbnailBehind,
  THUMBNAIL_SOURCE_LIMIT,
} from "@/lib/thumbnailSupport";
import { catalogHandlers } from "./handlers/catalogs";
import { apiBrowseHandlers } from "./handlers/apis";
import { userHandlers } from "./handlers/users";
import { connectionInstanceHandlers } from "./handlers/connections";
import { scriptHandlers } from "./handlers/scripts";
import { sessionHandlers } from "./handlers/sessions";
import { callHandlers } from "./handlers/calls";
import { assetRefHandlers, rewriteRefs } from "./handlers/assetRefs";
import { producerHandlers } from "./handlers/producers";
import {
  mockDataHubConnections,
  catalogBrowse,
  catalogSearch,
  catalogEntity,
  lookupTags,
  createTag,
  deleteTag,
  lookupGlossaryTerms,
  lookupDomains,
  createDomain,
  deleteDomain,
  glossaryRoots,
  glossaryChildren,
  glossaryParents,
  glossaryTerm,
  createGlossaryEntity,
  deleteGlossaryEntity,
  entityDocuments,
  applyCatalogChange,
  docsBrowse,
  docsSearch,
  getDoc,
  createDoc,
  updateDoc,
  deleteDoc,
} from "./data/datahub";
import {
  mockKnowledgePages,
  pageRefs,
  pagesReferencing,
  resolvePageRef,
  setPageRefs,
} from "./data/knowledgePages";
import { mockKnowledgeGraph } from "./data/knowledgeGraph";
import { mockContent } from "./data/content";
import { mockAllCollections, mockCollections, mockSharedCollections } from "./data/collections";
import {
  mockAdminPrompts,
  mockPortalPrompts,
  mockSharedPrompts,
  mockPromptCollections,
  mockPromptUsage,
  mockPromptVersions,
} from "./data/prompts";
import { mockResources, mockResourceUsage, mockResourceVersions } from "./data/resources";
import { resourceImageBytes } from "./data/resourceImages";
import {
  mockDropTable,
  mockRegisterTable,
  mockScratchTable,
  mockScratchTableList,
  mockTableConnections,
  mockTableRegistrations,
  tornCSVProblem,
} from "./data/tables";
import type { TableRegistration } from "@/api/tables/types";
import {
  mentionedThreadIDs,
  mockThreadChains,
  mockThreadEvents,
  mockThreads,
} from "./data/feedback";
import { mockAPIKeys } from "./data/keys";
import {
  mockEffectiveConfig,
  mockConfigEntries,
  mockConfigChangelog,
} from "./data/config";

import {
  mockPortalMemoryRecords,
  mockPortalMemoryStats,
} from "./data/memory";
import { promInstantFor, promRangeFor } from "./data/observability";
import { mockIndexJobsSummary, mockIndexJobs, mockIndexJobsFailures } from "./data/indexjobs";

// Mutable copy backing the stateful prompt-collection handlers (#1010); module
// state resets on page load, which is what the MSW-mode flows need.
const statefulPromptCollections = mockPromptCollections.map((c) => ({ ...c }));

// Mutable prompt-to-resource attachment map backing the #1013 handlers, keyed
// by prompt id and holding resource ids in authored order.
//
// "res-deleted" and "res-restricted" are deliberately absent from
// mockResources: the first renders the broken-link state an author must be able
// to see and clean up, and the second renders the restricted state, so both
// degraded paths are reachable in MSW mode without server-side setup.
const statefulPromptAttachments: Record<string, string[]> = {
  "prompt-010": ["res-001", "res-deleted"],
  "prompt-003": ["res-restricted"],
};

// RESTRICTED_RESOURCE_IDS stand in for resources that exist but sit outside the
// caller's scope. The server sends only the id and a flag for these.
const RESTRICTED_RESOURCE_IDS = new Set(["res-restricted"]);

// allMockPrompts flattens every prompt the mock serves, for the reverse lookup
// from a resource to the prompts that attach it.
function allMockPrompts() {
  return [
    ...mockPortalPrompts.personal,
    ...mockPortalPrompts.available,
    ...mockSharedPrompts.map((s) => s.prompt),
  ];
}

// promptAttachmentList renders the server's attachment view for one prompt,
// including the broken and restricted flags.
function promptAttachmentList(promptId: string) {
  const ids = statefulPromptAttachments[promptId] ?? [];
  const data = ids.map((resourceId, position) => {
    const base = { resource_id: resourceId, position, attached_by: "j.martinez@example.com" };
    if (RESTRICTED_RESOURCE_IDS.has(resourceId)) {
      return { ...base, unreadable: true };
    }
    const res = mockResources.resources.find((r) => r.id === resourceId);
    if (!res) {
      return { ...base, broken: true };
    }
    return {
      ...base,
      display_name: res.display_name,
      description: res.description,
      path: res.path,
      mime_type: res.mime_type,
      size_bytes: res.size_bytes,
      uri: res.uri,
      scope: res.scope,
      scope_id: res.scope_id,
    };
  });
  return { data, total: data.length };
}

const ADMIN_BASE = "/api/v1/admin";
const PORTAL_BASE = "/api/v1/portal";
const OBSERVABILITY_BASE = "/api/v1/observability";

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

// Deterministic string hash so tool activity figures are stable across reloads
// (stable screenshots) yet vary per tool.
function hashStr(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

// Synthesize the aggregated per-tool detail returned by GET /tools/:name.
// Mirrors the Go handler that fuses tool metadata, per-persona access, recent
// activity, and enrichment-rule counts into one payload for the Tools page.
function buildToolDetail(name: string): ToolDetail | null {
  const info = mockTools.find((t) => t.name === name);
  if (!info) return null;
  const schema = mockToolSchemas[name];
  const h = hashStr(name);

  const personas: ToolPersonaAccess[] = mockPersonas.map((p) => {
    const detail = mockPersonaDetails[p.name];
    const allowed =
      detail?.tools.includes(`${name}:${info.connection}`) ?? false;
    return {
      persona: p.name,
      allowed,
      connection_allowed: true,
      source: allowed ? "allow" : "deny",
      matched_pattern: allowed
        ? (detail?.allow_tools.find(
            (pat) =>
              pat === "*" ||
              (pat.endsWith("*") && name.startsWith(pat.slice(0, -1))) ||
              pat === name,
          ) ?? "*")
        : undefined,
    };
  });

  const isGateway = info.kind === "mcp";
  const ruleCount = isGateway
    ? (mockEnrichmentRules[info.connection]?.length ?? 0)
    : 0;

  return {
    name,
    title: schema?.title,
    description:
      schema?.description ?? `Tool ${name} on connection ${info.connection}.`,
    toolkit_kind: info.kind,
    toolkit_name: info.toolkit,
    connection: info.connection,
    input_schema: schema?.parameters,
    personas,
    hidden_by_global_deny: info.hidden ?? false,
    description_overridden: false,
    activity: {
      window_seconds: 86400,
      call_count: 80 + (h % 5200),
      success_rate: Math.min(0.999, 0.9 + ((h >> 4) % 100) / 1000),
      avg_duration_ms: 25 + ((h >> 7) % 900),
    },
    enrichment_rule_count: ruleCount,
  };
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

  const staleCutoff = Date.now() - 30 * 86_400_000;
  let oldestPendingAt: string | undefined;
  let pendingOver30d = 0;
  for (const ins of insights) {
    byStatus[ins.status] = (byStatus[ins.status] ?? 0) + 1;
    byCategory[ins.category] = (byCategory[ins.category] ?? 0) + 1;
    byConfidence[ins.confidence] = (byConfidence[ins.confidence] ?? 0) + 1;
    if (ins.status === "pending") {
      if (!oldestPendingAt || ins.created_at < oldestPendingAt) {
        oldestPendingAt = ins.created_at;
      }
      if (new Date(ins.created_at).getTime() <= staleCutoff) pendingOver30d++;
    }
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
    oldest_pending_at: oldestPendingAt,
    pending_over_30d: pendingOver30d,
  };
}

// ---------------------------------------------------------------------------
// Portal helpers
// ---------------------------------------------------------------------------

const portalAssets = [
  ...mockAssets,
  ...mockSharedWithMe.map((s) => s.asset),
];

const thumbnailStore = new Map<string, ArrayBuffer>();

/**
 * The light-scheme colors of the static thumbnail fixtures, and the dark ones
 * a capture of the same document would carry. They are the tokens the real
 * capturer draws with (components/thumbnail/schemes.ts), so a recolored fixture
 * stands in for the dark capture rather than inventing a second document.
 *
 * Several pairs are each other's inverse (#0f172a and #f8fafc swap), so the
 * substitution has to be a single pass over the document. Applying the rules in
 * turn would let a later one rewrite what an earlier one produced and land the
 * fixture back on a light color.
 */
const STATIC_THUMBNAIL_DARK_TOKENS: Record<string, string> = {
  white: "#131a25",
  "#ffffff": "#131a25",
  "#f0f2f5": "#131a25",
  "#f8fafc": "#0f172a",
  "#f1f5f9": "#1e293b",
  "#e2e8f0": "#334155",
  "#0f172a": "#f8fafc",
  "#1e293b": "#e2e8f0",
  "#334155": "#cbd5e1",
  "#64748b": "#94a3b8",
};

/** A static fixture recolored to the dark scheme. */
function darkenStaticThumbnail(svg: string): string {
  return svg.replace(
    /#[0-9a-fA-F]{6}\b|\bwhite\b/g,
    (token) => STATIC_THUMBNAIL_DARK_TOKENS[token.toLowerCase()] ?? token,
  );
}

/**
 * A collection with its items enriched the way the store's join enriches them
 * (getItemsBySections): each item carries the asset's name, content type, both
 * capture keys and both capture versions, so a tile can pick the variant the
 * reader's color mode needs (#1468).
 */
function withItemAssetFields<T extends { sections?: unknown[] }>(coll: T): T {
  return {
    ...coll,
    sections: ((coll.sections ?? []) as Record<string, unknown>[]).map((s) => ({
      ...s,
      items: ((s.items ?? []) as Record<string, unknown>[]).map((item) => {
        const asset = portalAssets.find((a) => a.id === item.asset_id);
        return {
          ...item,
          asset_name: item.asset_name ?? asset?.name,
          asset_content_type: item.asset_content_type ?? asset?.content_type,
          asset_description: item.asset_description ?? asset?.description,
          asset_thumbnail_s3_key: asset?.thumbnail_s3_key,
          asset_thumbnail_dark_s3_key: asset?.thumbnail_dark_s3_key,
          asset_thumbnail_version: asset?.thumbnail_version,
          asset_thumbnail_dark_version: asset?.thumbnail_dark_version,
        };
      }),
    })),
  };
}

/**
 * The thumbnail bytes for an asset and variant, or null when none exist.
 *
 * A dark request falls back to the light capture, which is what the server
 * does for a content type that carries its own colors and stores one image for
 * both modes.
 */
function serveThumbnail(id: string, variant: string | null): HttpResponse<BodyInit> | null {
  const dark = variant === "dark";
  const buffer = dark ? (thumbnailStore.get(`${id}:dark`) ?? thumbnailStore.get(id)) : thumbnailStore.get(id);
  if (buffer) {
    return new HttpResponse(buffer, { headers: { "Content-Type": "image/png" } });
  }
  const staticSvg = STATIC_THUMBNAILS[id];
  if (staticSvg) {
    const body = dark ? darkenStaticThumbnail(staticSvg) : staticSvg;
    return new HttpResponse(body, { headers: { "Content-Type": "image/svg+xml" } });
  }
  return null;
}

const STATIC_THUMBNAILS: Record<string, string> = {
  // ast-002 is the SVG pipeline chart. Without an entry here its thumbnail
  // request 404s, so every collection containing it composited one tile short
  // and the queue had a failing fetch in the middle of its run.
  "ast-002": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="#f8fafc"/>
<text x="16" y="28" font-family="system-ui" font-size="13" font-weight="700" fill="#0f172a">Sales Pipeline</text>
<text x="16" y="44" font-family="system-ui" font-size="8" fill="#64748b">Current quarter stages</text>
<rect x="16" y="60" width="368" height="34" rx="4" fill="#3b82f6"/>
<text x="28" y="82" font-family="system-ui" font-size="11" font-weight="600" fill="white">Leads: 1,240</text>
<rect x="40" y="102" width="320" height="34" rx="4" fill="#6366f1"/>
<text x="52" y="124" font-family="system-ui" font-size="11" font-weight="600" fill="white">Qualified: 680</text>
<rect x="64" y="144" width="272" height="34" rx="4" fill="#8b5cf6"/>
<text x="76" y="166" font-family="system-ui" font-size="11" font-weight="600" fill="white">Proposal: 310</text>
<rect x="88" y="186" width="224" height="34" rx="4" fill="#a855f7"/>
<text x="100" y="208" font-family="system-ui" font-size="11" font-weight="600" fill="white">Negotiation: 145</text>
<rect x="112" y="228" width="176" height="34" rx="4" fill="#22c55e"/>
<text x="124" y="250" font-family="system-ui" font-size="11" font-weight="600" fill="white">Won: 82</text>
</svg>`,
  "ast-001": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<defs><linearGradient id="hdr1" x1="0" y1="0" x2="1" y2="1"><stop offset="0%" stop-color="#1e293b"/><stop offset="100%" stop-color="#334155"/></linearGradient></defs>
<rect width="400" height="300" fill="#f0f2f5"/>
<rect x="0" y="0" width="400" height="45" fill="url(#hdr1)"/>
<text x="15" y="20" font-family="system-ui" font-size="10" font-weight="700" fill="white">Q4 2025 Revenue Dashboard</text>
<text x="15" y="33" font-family="system-ui" font-size="6" fill="#94a3b8">Generated from warehouse data</text>
<rect x="330" y="10" width="55" height="16" rx="8" fill="#22c55e" opacity="0.2"/><text x="340" y="21" font-family="system-ui" font-size="6" font-weight="600" fill="#22c55e">LIVE DATA</text>
<rect x="10" y="52" width="92" height="48" rx="5" fill="white" stroke="#e2e8f0"/>
<rect x="10" y="52" width="92" height="3" rx="1" fill="#3b82f6"/>
<text x="16" y="67" font-family="system-ui" font-size="5" fill="#94a3b8">TOTAL REVENUE</text>
<text x="16" y="85" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">$4.2M</text>
<text x="65" y="67" font-family="system-ui" font-size="5" fill="#16a34a">+12.3%</text>
<rect x="107" y="52" width="92" height="48" rx="5" fill="white" stroke="#e2e8f0"/>
<rect x="107" y="52" width="92" height="3" rx="1" fill="#8b5cf6"/>
<text x="113" y="67" font-family="system-ui" font-size="5" fill="#94a3b8">AVG ORDER</text>
<text x="113" y="85" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">$847</text>
<rect x="204" y="52" width="92" height="48" rx="5" fill="white" stroke="#e2e8f0"/>
<rect x="204" y="52" width="92" height="3" rx="1" fill="#10b981"/>
<text x="210" y="67" font-family="system-ui" font-size="5" fill="#94a3b8">TOTAL ORDERS</text>
<text x="210" y="85" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">4,958</text>
<rect x="301" y="52" width="92" height="48" rx="5" fill="white" stroke="#e2e8f0"/>
<rect x="301" y="52" width="92" height="3" rx="1" fill="#f59e0b"/>
<text x="307" y="67" font-family="system-ui" font-size="5" fill="#94a3b8">RETURN RATE</text>
<text x="307" y="85" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">3.2%</text>
<rect x="10" y="108" width="185" height="90" rx="5" fill="white" stroke="#e2e8f0"/>
<text x="16" y="122" font-family="system-ui" font-size="7" font-weight="700" fill="#0f172a">Revenue by Region</text>
<text x="16" y="140" font-family="system-ui" font-size="6" fill="#334155">West</text><rect x="50" y="134" width="120" height="8" rx="2" fill="#3b82f6"/><text x="174" y="141" font-family="system-ui" font-size="5" fill="#64748b">$1.54M</text>
<text x="16" y="154" font-family="system-ui" font-size="6" fill="#334155">East</text><rect x="50" y="148" width="98" height="8" rx="2" fill="#6366f1"/><text x="152" y="155" font-family="system-ui" font-size="5" fill="#64748b">$1.26M</text>
<text x="16" y="168" font-family="system-ui" font-size="6" fill="#334155">Central</text><rect x="50" y="162" width="69" height="8" rx="2" fill="#8b5cf6"/><text x="123" y="169" font-family="system-ui" font-size="5" fill="#64748b">$890K</text>
<text x="16" y="182" font-family="system-ui" font-size="6" fill="#334155">South</text><rect x="50" y="176" width="50" height="8" rx="2" fill="#a78bfa"/><text x="104" y="183" font-family="system-ui" font-size="5" fill="#64748b">$640K</text>
<rect x="205" y="108" width="185" height="90" rx="5" fill="white" stroke="#e2e8f0"/>
<text x="211" y="122" font-family="system-ui" font-size="7" font-weight="700" fill="#0f172a">Top Products</text>
<text x="211" y="140" font-family="system-ui" font-size="6" fill="#334155">Enterprise Suite Pro</text><text x="360" y="140" font-family="system-ui" font-size="5" fill="#16a34a">Trending</text>
<text x="211" y="154" font-family="system-ui" font-size="6" fill="#334155">CloudSync Platform</text><text x="360" y="154" font-family="system-ui" font-size="5" fill="#16a34a">Trending</text>
<text x="211" y="168" font-family="system-ui" font-size="6" fill="#334155">DataVault Storage</text><text x="360" y="168" font-family="system-ui" font-size="5" fill="#3b82f6">Stable</text>
<text x="211" y="182" font-family="system-ui" font-size="6" fill="#334155">Analytics Core</text><text x="360" y="182" font-family="system-ui" font-size="5" fill="#16a34a">Trending</text>
<rect x="10" y="206" width="383" height="85" rx="5" fill="white" stroke="#e2e8f0"/>
<text x="16" y="220" font-family="system-ui" font-size="7" font-weight="700" fill="#0f172a">Monthly Revenue Trend</text>
<polyline points="30,270 60,265 90,258 120,260 150,250 180,245 210,240 240,235 270,228 300,220 330,210 360,200" fill="none" stroke="#3b82f6" stroke-width="2"/>
<line x1="30" y1="275" x2="370" y2="275" stroke="#e2e8f0"/>
<text x="30" y="285" font-family="system-ui" font-size="5" fill="#94a3b8">Jan</text>
<text x="120" y="285" font-family="system-ui" font-size="5" fill="#94a3b8">Apr</text>
<text x="210" y="285" font-family="system-ui" font-size="5" fill="#94a3b8">Jul</text>
<text x="300" y="285" font-family="system-ui" font-size="5" fill="#94a3b8">Oct</text>
</svg>`,
  "ast-003": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="white"/>
<text x="16" y="24" font-family="system-ui" font-size="12" font-weight="700" fill="#0f172a">Weekly Inventory Report</text>
<text x="16" y="40" font-family="system-ui" font-size="7" fill="#64748b">Week of 4/15/2026</text>
<text x="16" y="62" font-family="system-ui" font-size="9" font-weight="600" fill="#1e293b">Summary</text>
<rect x="16" y="70" width="200" height="14" fill="#f1f5f9" rx="2"/>
<text x="20" y="80" font-family="monospace" font-size="6" font-weight="600" fill="#334155">Metric</text>
<text x="100" y="80" font-family="monospace" font-size="6" font-weight="600" fill="#334155">Value</text>
<text x="155" y="80" font-family="monospace" font-size="6" font-weight="600" fill="#334155">Change</text>
<text x="20" y="96" font-family="monospace" font-size="6" fill="#334155">Total SKUs</text><text x="100" y="96" font-family="monospace" font-size="6" fill="#334155">12,450</text><text x="155" y="96" font-family="monospace" font-size="6" fill="#16a34a">+120</text>
<text x="20" y="110" font-family="monospace" font-size="6" fill="#334155">In Stock</text><text x="100" y="110" font-family="monospace" font-size="6" fill="#334155">11,200</text><text x="155" y="110" font-family="monospace" font-size="6" fill="#16a34a">+95</text>
<text x="20" y="124" font-family="monospace" font-size="6" fill="#334155">Low Stock</text><text x="100" y="124" font-family="monospace" font-size="6" fill="#334155">890</text><text x="155" y="124" font-family="monospace" font-size="6" fill="#ef4444">-30</text>
<text x="20" y="138" font-family="monospace" font-size="6" fill="#334155">Out of Stock</text><text x="100" y="138" font-family="monospace" font-size="6" fill="#334155">360</text><text x="155" y="138" font-family="monospace" font-size="6" fill="#ef4444">+55</text>
<text x="16" y="164" font-family="system-ui" font-size="9" font-weight="600" fill="#1e293b">Warehouse Breakdown</text>
<text x="16" y="180" font-family="system-ui" font-size="7" fill="#334155">West Distribution Center</text>
<rect x="16" y="186" width="160" height="6" rx="2" fill="#10b981" opacity="0.7"/>
<text x="180" y="192" font-family="system-ui" font-size="6" fill="#64748b">94% stocked</text>
<text x="16" y="206" font-family="system-ui" font-size="7" fill="#334155">East Distribution Center</text>
<rect x="16" y="212" width="140" height="6" rx="2" fill="#3b82f6" opacity="0.7"/>
<text x="160" y="218" font-family="system-ui" font-size="6" fill="#64748b">87% stocked</text>
<text x="16" y="238" font-family="system-ui" font-size="7" fill="#334155">Central Warehouse</text>
<rect x="16" y="244" width="120" height="6" rx="2" fill="#f59e0b" opacity="0.7"/>
<text x="140" y="250" font-family="system-ui" font-size="6" fill="#64748b">78% stocked</text>
</svg>`,
  "ast-005": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="white"/>
<text x="16" y="24" font-family="system-ui" font-size="11" font-weight="700" fill="#0f172a">Customer Segmentation Analysis</text>
<text x="16" y="40" font-family="system-ui" font-size="7" fill="#64748b">Purchasing behavior patterns by segment</text>
<rect x="16" y="52" width="368" height="16" fill="#f1f5f9" rx="2"/>
<text x="22" y="63" font-family="system-ui" font-size="6" font-weight="600" fill="#334155">Segment</text>
<text x="120" y="63" font-family="system-ui" font-size="6" font-weight="600" fill="#334155">Customers</text>
<text x="200" y="63" font-family="system-ui" font-size="6" font-weight="600" fill="#334155">Avg Spend</text>
<text x="280" y="63" font-family="system-ui" font-size="6" font-weight="600" fill="#334155">Frequency</text>
<text x="350" y="63" font-family="system-ui" font-size="6" font-weight="600" fill="#334155">Trend</text>
<text x="22" y="82" font-family="system-ui" font-size="7" fill="#334155">Champions</text><text x="120" y="82" font-family="system-ui" font-size="7" fill="#334155">8,420</text><text x="200" y="82" font-family="system-ui" font-size="7" fill="#334155">$2,340</text><text x="280" y="82" font-family="system-ui" font-size="7" fill="#334155">Weekly</text><text x="350" y="82" font-family="system-ui" font-size="6" fill="#16a34a">+12%</text>
<text x="22" y="100" font-family="system-ui" font-size="7" fill="#334155">Loyal Customers</text><text x="120" y="100" font-family="system-ui" font-size="7" fill="#334155">15,680</text><text x="200" y="100" font-family="system-ui" font-size="7" fill="#334155">$890</text><text x="280" y="100" font-family="system-ui" font-size="7" fill="#334155">Bi-weekly</text><text x="350" y="100" font-family="system-ui" font-size="6" fill="#16a34a">+5%</text>
<text x="22" y="118" font-family="system-ui" font-size="7" fill="#334155">Potential Loyalists</text><text x="120" y="118" font-family="system-ui" font-size="7" fill="#334155">22,100</text><text x="200" y="118" font-family="system-ui" font-size="7" fill="#334155">$420</text><text x="280" y="118" font-family="system-ui" font-size="7" fill="#334155">Monthly</text><text x="350" y="118" font-family="system-ui" font-size="6" fill="#3b82f6">+2%</text>
<text x="22" y="136" font-family="system-ui" font-size="7" fill="#334155">At Risk</text><text x="120" y="136" font-family="system-ui" font-size="7" fill="#334155">5,200</text><text x="200" y="136" font-family="system-ui" font-size="7" fill="#334155">$180</text><text x="280" y="136" font-family="system-ui" font-size="7" fill="#334155">Quarterly</text><text x="350" y="136" font-family="system-ui" font-size="6" fill="#ef4444">-8%</text>
<text x="22" y="154" font-family="system-ui" font-size="7" fill="#334155">Hibernating</text><text x="120" y="154" font-family="system-ui" font-size="7" fill="#334155">12,840</text><text x="200" y="154" font-family="system-ui" font-size="7" fill="#334155">$65</text><text x="280" y="154" font-family="system-ui" font-size="7" fill="#334155">Rare</text><text x="350" y="154" font-family="system-ui" font-size="6" fill="#ef4444">-15%</text>
<rect x="16" y="170" width="180" height="120" rx="5" fill="#f8fafc" stroke="#e2e8f0"/>
<text x="22" y="186" font-family="system-ui" font-size="7" font-weight="600" fill="#0f172a">Segment Distribution</text>
<circle cx="90" cy="240" r="40" fill="none" stroke="#6366f1" stroke-width="20" stroke-dasharray="25 75" stroke-dashoffset="0"/>
<circle cx="90" cy="240" r="40" fill="none" stroke="#3b82f6" stroke-width="20" stroke-dasharray="24 76" stroke-dashoffset="-25"/>
<circle cx="90" cy="240" r="40" fill="none" stroke="#10b981" stroke-width="20" stroke-dasharray="34 66" stroke-dashoffset="-49"/>
</svg>`,
  "ast-006": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="white"/>
<text x="16" y="24" font-family="system-ui" font-size="12" font-weight="700" fill="#0f172a">Data Quality Summary</text>
<text x="16" y="40" font-family="system-ui" font-size="7" fill="#64748b">Last updated: 4/15/2026</text>
<text x="16" y="62" font-family="system-ui" font-size="9" font-weight="600" fill="#1e293b">Overall Health</text>
<rect x="16" y="70" width="300" height="14" fill="#f1f5f9" rx="2"/>
<text x="22" y="80" font-family="monospace" font-size="6" font-weight="600" fill="#334155">Score</text>
<text x="70" y="80" font-family="monospace" font-size="6" font-weight="600" fill="#334155">Category</text>
<text x="160" y="80" font-family="monospace" font-size="6" font-weight="600" fill="#334155">Details</text>
<text x="22" y="96" font-family="system-ui" font-size="7" font-weight="700" fill="#16a34a">94%</text><text x="70" y="96" font-family="system-ui" font-size="7" fill="#334155">Completeness</text><text x="160" y="96" font-family="system-ui" font-size="6" fill="#64748b">6% null values in optional fields</text>
<text x="22" y="112" font-family="system-ui" font-size="7" font-weight="700" fill="#16a34a">99%</text><text x="70" y="112" font-family="system-ui" font-size="7" fill="#334155">Uniqueness</text><text x="160" y="112" font-family="system-ui" font-size="6" fill="#64748b">12 duplicate records found in staging</text>
<text x="22" y="128" font-family="system-ui" font-size="7" font-weight="700" fill="#16a34a">97%</text><text x="70" y="128" font-family="system-ui" font-size="7" fill="#334155">Timeliness</text><text x="160" y="128" font-family="system-ui" font-size="6" fill="#64748b">All pipelines within SLA</text>
<text x="22" y="144" font-family="system-ui" font-size="7" font-weight="700" fill="#f59e0b">88%</text><text x="70" y="144" font-family="system-ui" font-size="7" fill="#334155">Accuracy</text><text x="160" y="144" font-family="system-ui" font-size="6" fill="#64748b">3 tables flagged for review</text>
<text x="16" y="170" font-family="system-ui" font-size="9" font-weight="600" fill="#1e293b">Flagged Tables</text>
<rect x="16" y="178" width="300" height="20" rx="4" fill="#fef2f2" stroke="#fecaca"/>
<text x="22" y="192" font-family="monospace" font-size="7" fill="#dc2626">analytics.daily_sales</text><text x="200" y="192" font-family="system-ui" font-size="6" fill="#dc2626">Missing data for stores 401-405</text>
<rect x="16" y="204" width="300" height="20" rx="4" fill="#fffbeb" stroke="#fed7aa"/>
<text x="22" y="218" font-family="monospace" font-size="7" fill="#d97706">inventory.levels</text><text x="200" y="218" font-family="system-ui" font-size="6" fill="#d97706">Duplicate rows for WH-07</text>
<rect x="16" y="230" width="300" height="20" rx="4" fill="#fffbeb" stroke="#fed7aa"/>
<text x="22" y="244" font-family="monospace" font-size="7" fill="#d97706">finance.price_adjustments</text><text x="200" y="244" font-family="system-ui" font-size="6" fill="#d97706">12 negative discount amounts</text>
</svg>`,
  "ast-007": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="#f7fafc"/>
<rect x="0" y="0" width="400" height="40" fill="#1a365d"/>
<text x="15" y="18" font-family="system-ui" font-size="9" font-weight="700" fill="white">ACME Corp Sales Dashboard</text>
<text x="15" y="30" font-family="system-ui" font-size="6" fill="#a0aec0">Annual Performance Overview</text>
<rect x="10" y="48" width="120" height="44" rx="4" fill="white" stroke="#e2e8f0"/>
<text x="16" y="62" font-family="system-ui" font-size="5" fill="#718096">Revenue YTD</text>
<text x="16" y="80" font-family="system-ui" font-size="14" font-weight="800" fill="#1a365d">$5.37M</text>
<rect x="140" y="48" width="120" height="44" rx="4" fill="white" stroke="#e2e8f0"/>
<text x="146" y="62" font-family="system-ui" font-size="5" fill="#718096">Stores Active</text>
<text x="146" y="80" font-family="system-ui" font-size="14" font-weight="800" fill="#1a365d">1.50M</text>
<rect x="270" y="48" width="120" height="44" rx="4" fill="white" stroke="#e2e8f0"/>
<text x="276" y="62" font-family="system-ui" font-size="5" fill="#718096">Avg Transaction</text>
<text x="276" y="80" font-family="system-ui" font-size="14" font-weight="800" fill="#1a365d">$3.59</text>
<rect x="10" y="100" width="250" height="100" rx="4" fill="white" stroke="#e2e8f0"/>
<text x="16" y="114" font-family="system-ui" font-size="7" font-weight="600" fill="#2d3748">Revenue by Region</text>
<rect x="20" y="124" width="100" height="8" rx="2" fill="#1a365d"/><text x="124" y="131" font-family="system-ui" font-size="5" fill="#718096">Midwest $1.2M</text>
<rect x="20" y="138" width="80" height="8" rx="2" fill="#2b6cb0"/><text x="104" y="145" font-family="system-ui" font-size="5" fill="#718096">West $980K</text>
<rect x="20" y="152" width="70" height="8" rx="2" fill="#3182ce"/><text x="94" y="159" font-family="system-ui" font-size="5" fill="#718096">East $870K</text>
<rect x="20" y="166" width="55" height="8" rx="2" fill="#e53e3e"/><text x="79" y="173" font-family="system-ui" font-size="5" fill="#718096">South $640K</text>
<rect x="270" y="100" width="120" height="100" rx="4" fill="white" stroke="#e2e8f0"/>
<text x="276" y="114" font-family="system-ui" font-size="7" font-weight="600" fill="#2d3748">Category Mix</text>
<circle cx="330" cy="160" r="30" fill="none" stroke="#1a365d" stroke-width="15" stroke-dasharray="30 70"/>
<circle cx="330" cy="160" r="30" fill="none" stroke="#e53e3e" stroke-width="15" stroke-dasharray="20 80" stroke-dashoffset="-30"/>
<circle cx="330" cy="160" r="30" fill="none" stroke="#38a169" stroke-width="15" stroke-dasharray="25 75" stroke-dashoffset="-50"/>
</svg>`,
  "ast-004": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<defs><linearGradient id="bg4" x1="0" y1="0" x2="1" y2="1"><stop offset="0%" stop-color="#f0f4ff"/><stop offset="100%" stop-color="#f0fdf4"/></linearGradient></defs>
<rect width="400" height="300" fill="url(#bg4)"/>
<text x="20" y="24" font-family="system-ui" font-size="11" font-weight="700" fill="#312e81">ACME Corp Store Performance</text>
<text x="20" y="38" font-family="system-ui" font-size="7" fill="#94a3b8">Q1 2026 Consolidated Metrics</text>
<rect x="15" y="48" width="118" height="55" rx="6" fill="white" stroke="#e2e8f0"/>
<rect x="15" y="48" width="118" height="3" rx="1" fill="#6366f1"/>
<text x="22" y="64" font-family="system-ui" font-size="6" fill="#94a3b8" text-transform="uppercase">REVENUE</text>
<text x="22" y="82" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">$1.54M</text>
<text x="22" y="95" font-family="system-ui" font-size="6" fill="#16a34a">+15.2%</text>
<rect x="140" y="48" width="118" height="55" rx="6" fill="white" stroke="#e2e8f0"/>
<rect x="140" y="48" width="118" height="3" rx="1" fill="#3b82f6"/>
<text x="147" y="64" font-family="system-ui" font-size="6" fill="#94a3b8">TRANSACTIONS</text>
<text x="147" y="82" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">12,847</text>
<text x="147" y="95" font-family="system-ui" font-size="6" fill="#16a34a">+8.3%</text>
<rect x="265" y="48" width="118" height="55" rx="6" fill="white" stroke="#e2e8f0"/>
<rect x="265" y="48" width="118" height="3" rx="1" fill="#f59e0b"/>
<text x="272" y="64" font-family="system-ui" font-size="6" fill="#94a3b8">AVG BASKET</text>
<text x="272" y="82" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">$119.80</text>
<text x="272" y="95" font-family="system-ui" font-size="6" fill="#dc2626">-2.1%</text>
<rect x="15" y="112" width="118" height="55" rx="6" fill="white" stroke="#e2e8f0"/>
<rect x="15" y="112" width="118" height="3" rx="1" fill="#10b981"/>
<text x="22" y="128" font-family="system-ui" font-size="6" fill="#94a3b8">FOOTFALL</text>
<text x="22" y="146" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">45,200</text>
<text x="22" y="159" font-family="system-ui" font-size="6" fill="#16a34a">+11.7%</text>
<rect x="140" y="112" width="118" height="55" rx="6" fill="white" stroke="#e2e8f0"/>
<rect x="140" y="112" width="118" height="3" rx="1" fill="#8b5cf6"/>
<text x="147" y="128" font-family="system-ui" font-size="6" fill="#94a3b8">CONVERSION</text>
<text x="147" y="146" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">28.4%</text>
<text x="147" y="159" font-family="system-ui" font-size="6" fill="#16a34a">+3.2%</text>
<rect x="265" y="112" width="118" height="55" rx="6" fill="white" stroke="#e2e8f0"/>
<rect x="265" y="112" width="118" height="3" rx="1" fill="#06b6d4"/>
<text x="272" y="128" font-family="system-ui" font-size="6" fill="#94a3b8">RETURN RATE</text>
<text x="272" y="146" font-family="system-ui" font-size="16" font-weight="800" fill="#0f172a">2.8%</text>
<text x="272" y="159" font-family="system-ui" font-size="6" fill="#16a34a">-0.5%</text>
<rect x="15" y="178" width="368" height="110" rx="6" fill="white" stroke="#e2e8f0"/>
<text x="22" y="195" font-family="system-ui" font-size="8" font-weight="700" fill="#0f172a">Top Selling Categories</text>
<text x="22" y="216" font-family="system-ui" font-size="7" fill="#334155">Electronics</text>
<rect x="100" y="209" width="136" height="10" rx="3" fill="#6366f1" opacity="0.8"/>
<text x="240" y="217" font-family="system-ui" font-size="7" font-weight="600" fill="#0f172a">34%</text>
<text x="22" y="234" font-family="system-ui" font-size="7" fill="#334155">Home &amp; Garden</text>
<rect x="100" y="227" width="88" height="10" rx="3" fill="#10b981" opacity="0.8"/>
<text x="192" y="235" font-family="system-ui" font-size="7" font-weight="600" fill="#0f172a">22%</text>
<text x="22" y="252" font-family="system-ui" font-size="7" fill="#334155">Sporting Goods</text>
<rect x="100" y="245" width="72" height="10" rx="3" fill="#f59e0b" opacity="0.8"/>
<text x="176" y="253" font-family="system-ui" font-size="7" font-weight="600" fill="#0f172a">18%</text>
<text x="22" y="270" font-family="system-ui" font-size="7" fill="#334155">Seasonal</text>
<rect x="100" y="263" width="60" height="10" rx="3" fill="#ef4444" opacity="0.8"/>
<text x="164" y="271" font-family="system-ui" font-size="7" font-weight="600" fill="#0f172a">15%</text>
</svg>`,
  "ast-008": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="#f8fafc"/>
<text x="20" y="24" font-family="system-ui" font-size="10" font-weight="700" fill="#0f172a">Regional Sales Summary</text>
<text x="20" y="38" font-family="system-ui" font-size="7" fill="#64748b">Quarterly sales by region</text>
<rect x="15" y="48" width="370" height="20" fill="#f1f5f9" rx="2"/>
<text x="22" y="61" font-family="monospace" font-size="7" font-weight="600" fill="#334155">Region</text>
<text x="100" y="61" font-family="monospace" font-size="7" font-weight="600" fill="#334155">Quarter</text>
<text x="180" y="61" font-family="monospace" font-size="7" font-weight="600" fill="#334155">Revenue</text>
<text x="270" y="61" font-family="monospace" font-size="7" font-weight="600" fill="#334155">Units</text>
<text x="340" y="61" font-family="monospace" font-size="7" font-weight="600" fill="#334155">Growth</text>
<line x1="15" y1="68" x2="385" y2="68" stroke="#e2e8f0"/>
<text x="22" y="84" font-family="monospace" font-size="7" fill="#334155">West</text>
<text x="100" y="84" font-family="monospace" font-size="7" fill="#334155">Q4-2025</text>
<text x="180" y="84" font-family="monospace" font-size="7" fill="#334155">$1,540,000</text>
<text x="270" y="84" font-family="monospace" font-size="7" fill="#334155">1,820</text>
<text x="340" y="84" font-family="monospace" font-size="7" fill="#16a34a">+15.2%</text>
<line x1="15" y1="90" x2="385" y2="90" stroke="#f1f5f9"/>
<text x="22" y="106" font-family="monospace" font-size="7" fill="#334155">East</text>
<text x="100" y="106" font-family="monospace" font-size="7" fill="#334155">Q4-2025</text>
<text x="180" y="106" font-family="monospace" font-size="7" fill="#334155">$1,260,000</text>
<text x="270" y="106" font-family="monospace" font-size="7" fill="#334155">1,488</text>
<text x="340" y="106" font-family="monospace" font-size="7" fill="#16a34a">+11.8%</text>
<line x1="15" y1="112" x2="385" y2="112" stroke="#f1f5f9"/>
<text x="22" y="128" font-family="monospace" font-size="7" fill="#334155">Central</text>
<text x="100" y="128" font-family="monospace" font-size="7" fill="#334155">Q4-2025</text>
<text x="180" y="128" font-family="monospace" font-size="7" fill="#334155">$890,000</text>
<text x="270" y="128" font-family="monospace" font-size="7" fill="#334155">1,050</text>
<text x="340" y="128" font-family="monospace" font-size="7" fill="#16a34a">+9.4%</text>
<line x1="15" y1="134" x2="385" y2="134" stroke="#f1f5f9"/>
<text x="22" y="150" font-family="monospace" font-size="7" fill="#334155">South</text>
<text x="100" y="150" font-family="monospace" font-size="7" fill="#334155">Q4-2025</text>
<text x="180" y="150" font-family="monospace" font-size="7" fill="#334155">$640,000</text>
<text x="270" y="150" font-family="monospace" font-size="7" fill="#334155">600</text>
<text x="340" y="150" font-family="monospace" font-size="7" fill="#16a34a">+7.1%</text>
<line x1="15" y1="156" x2="385" y2="156" stroke="#f1f5f9"/>
<text x="22" y="172" font-family="monospace" font-size="7" fill="#334155">Northwest</text>
<text x="100" y="172" font-family="monospace" font-size="7" fill="#334155">Q4-2025</text>
<text x="180" y="172" font-family="monospace" font-size="7" fill="#334155">$510,000</text>
<text x="270" y="172" font-family="monospace" font-size="7" fill="#334155">520</text>
<text x="340" y="172" font-family="monospace" font-size="7" fill="#16a34a">+6.8%</text>
<line x1="15" y1="178" x2="385" y2="178" stroke="#f1f5f9"/>
<text x="22" y="194" font-family="monospace" font-size="7" fill="#334155">Southeast</text>
<text x="100" y="194" font-family="monospace" font-size="7" fill="#334155">Q4-2025</text>
<text x="180" y="194" font-family="monospace" font-size="7" fill="#334155">$438,000</text>
<text x="270" y="194" font-family="monospace" font-size="7" fill="#334155">480</text>
<text x="340" y="194" font-family="monospace" font-size="7" fill="#16a34a">+5.3%</text>
<line x1="15" y1="200" x2="385" y2="200" stroke="#f1f5f9"/>
<text x="22" y="216" font-family="monospace" font-size="7" fill="#334155">Midwest</text>
<text x="100" y="216" font-family="monospace" font-size="7" fill="#334155">Q4-2025</text>
<text x="180" y="216" font-family="monospace" font-size="7" fill="#334155">$372,000</text>
<text x="270" y="216" font-family="monospace" font-size="7" fill="#334155">410</text>
<text x="340" y="216" font-family="monospace" font-size="7" fill="#16a34a">+4.1%</text>
<line x1="15" y1="222" x2="385" y2="222" stroke="#f1f5f9"/>
<text x="22" y="238" font-family="monospace" font-size="7" fill="#334155">Southwest</text>
<text x="100" y="238" font-family="monospace" font-size="7" fill="#334155">Q4-2025</text>
<text x="180" y="238" font-family="monospace" font-size="7" fill="#334155">$350,000</text>
<text x="270" y="238" font-family="monospace" font-size="7" fill="#334155">390</text>
<text x="340" y="238" font-family="monospace" font-size="7" fill="#16a34a">+3.8%</text>
</svg>`,

  "ast-009": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="#ffffff"/>
<text x="16" y="24" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">{</tspan></text>
<text x="16" y="37" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;</tspan><tspan fill="#0369a1">&quot;generated_at&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#047857">&quot;2026-08-19T06:00:00Z&quot;</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="50" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;</tspan><tspan fill="#0369a1">&quot;window&quot;</tspan><tspan fill="#6b7280">:&#160;{</tspan></text>
<text x="16" y="63" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;from&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#047857">&quot;2026-08-12&quot;</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="76" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;to&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#047857">&quot;2026-08-18&quot;</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="89" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;grain&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#047857">&quot;day&quot;</tspan></text>
<text x="16" y="102" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;},</tspan></text>
<text x="16" y="115" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;</tspan><tspan fill="#0369a1">&quot;totals&quot;</tspan><tspan fill="#6b7280">:&#160;{</tspan></text>
<text x="16" y="128" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;revenue&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#6d28d9">1284900.55</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="141" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;orders&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#6d28d9">18422</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="154" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;refunds&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#6d28d9">214</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="167" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;refund_rate&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#6d28d9">0.0116</tspan></text>
<text x="16" y="180" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;},</tspan></text>
<text x="16" y="193" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;</tspan><tspan fill="#0369a1">&quot;stores&quot;</tspan><tspan fill="#6b7280">:&#160;[</tspan></text>
<text x="16" y="206" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;{</tspan></text>
<text x="16" y="219" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;id&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#047857">&quot;STR-014&quot;</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="232" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;name&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#047857">&quot;Downtown&quot;</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="245" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;revenue&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#6d28d9">412300.1</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="258" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;orders&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#6d28d9">5921</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="271" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;open&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#b45309">true</tspan><tspan fill="#6b7280">,</tspan></text>
<text x="16" y="284" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">&#160;&#160;&#160;&#160;&#160;&#160;</tspan><tspan fill="#0369a1">&quot;manager&quot;</tspan><tspan fill="#6b7280">:&#160;</tspan><tspan fill="#b45309">null</tspan></text>
</svg>`,

  "ast-010": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="#ffffff"/>
<text x="26" y="24" font-family="monospace" font-size="8" text-anchor="end" fill="#6b7280">1</text>
<text x="34" y="24" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">{</tspan><tspan fill="#0369a1">&quot;event&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;checkout_started&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;ts&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;2026-08-18T14:02:11Z&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;store&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;STR-014&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;cart_value&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#6d28d9">128.4</tspan><tspan fill="#6b7280">}</tspan></text>
<text x="26" y="39" font-family="monospace" font-size="8" text-anchor="end" fill="#6b7280">2</text>
<text x="34" y="39" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">{</tspan><tspan fill="#0369a1">&quot;event&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;payment_authorized&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;ts&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;2026-08-18T14:02:44Z&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;store&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;STR-014&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;amount&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#6d28d9">128.4</tspan><tspan fill="#6b7280">}</tspan></text>
<text x="26" y="54" font-family="monospace" font-size="8" text-anchor="end" fill="#6b7280">3</text>
<text x="34" y="54" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">{</tspan><tspan fill="#0369a1">&quot;event&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;checkout_completed&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;ts&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;2026-08-18T14:02:51Z&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;store&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;STR-014&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;order_id&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;ORD-99120&quot;</tspan><tspan fill="#6b7280">}</tspan></text>
<text x="26" y="69" font-family="monospace" font-size="8" text-anchor="end" fill="#6b7280">4</text>
<text x="34" y="69" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">{</tspan><tspan fill="#0369a1">&quot;event&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;checkout_started&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;ts&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;2026-08-18T14:05:02Z&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;store&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;STR-027&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;cart_value&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#6d28d9">64.99</tspan><tspan fill="#6b7280">}</tspan></text>
<text x="26" y="84" font-family="monospace" font-size="8" text-anchor="end" fill="#6b7280">5</text>
<text x="34" y="84" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">{</tspan><tspan fill="#0369a1">&quot;event&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;payment_declined&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;ts&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;2026-08-18T14:05:30Z&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;store&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;STR-027&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;retryable&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#b45309">true</tspan><tspan fill="#6b7280">}</tspan></text>
<text x="26" y="99" font-family="monospace" font-size="8" text-anchor="end" fill="#6b7280">6</text>
<text x="34" y="99" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">{</tspan><tspan fill="#0369a1">&quot;event&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;checkout_abandoned&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;ts&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;2026-08-18T14:09:30Z&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;store&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;STR-027&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;order_id&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#b45309">null</tspan><tspan fill="#6b7280">}</tspan></text>
<text x="26" y="114" font-family="monospace" font-size="8" text-anchor="end" fill="#6b7280">7</text>
<text x="34" y="114" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">{</tspan><tspan fill="#0369a1">&quot;event&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;checkout_started&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;ts&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;2026-08-18T14:11:17Z&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;store&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;STR-031&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;cart_value&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#6d28d9">902.15</tspan><tspan fill="#6b7280">}</tspan></text>
<text x="26" y="129" font-family="monospace" font-size="8" text-anchor="end" fill="#6b7280">8</text>
<text x="34" y="129" font-family="monospace" font-size="8" xml:space="preserve"><tspan fill="#6b7280">{</tspan><tspan fill="#0369a1">&quot;event&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;checkout_completed&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;ts&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;2026-08-18T14:12:03Z&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;store&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;STR-031&quot;</tspan><tspan fill="#6b7280">,</tspan><tspan fill="#0369a1">&quot;coupon&quot;</tspan><tspan fill="#6b7280">:</tspan><tspan fill="#047857">&quot;FALL10&quot;</tspan><tspan fill="#6b7280">}</tspan></text>
</svg>`,

  "ast-ext-002": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="#f8fafc"/>
<text x="20" y="24" font-family="system-ui" font-size="10" font-weight="700" fill="#0f172a">API Latency Report</text>
<text x="20" y="38" font-family="system-ui" font-size="7" fill="#64748b">Response times by endpoint</text>
<rect x="15" y="48" width="370" height="18" fill="#f1f5f9" rx="2"/>
<text x="22" y="60" font-family="monospace" font-size="7" font-weight="600" fill="#334155">Endpoint</text>
<text x="150" y="60" font-family="monospace" font-size="7" font-weight="600" fill="#334155">p50</text>
<text x="210" y="60" font-family="monospace" font-size="7" font-weight="600" fill="#334155">p95</text>
<text x="270" y="60" font-family="monospace" font-size="7" font-weight="600" fill="#334155">p99</text>
<text x="330" y="60" font-family="monospace" font-size="7" font-weight="600" fill="#334155">Status</text>
<text x="22" y="82" font-family="monospace" font-size="7" fill="#334155">/api/v1/query</text>
<text x="150" y="82" font-family="monospace" font-size="7" fill="#334155">45ms</text>
<text x="210" y="82" font-family="monospace" font-size="7" fill="#334155">120ms</text>
<text x="270" y="82" font-family="monospace" font-size="7" fill="#334155">340ms</text>
<rect x="330" y="74" width="24" height="12" rx="6" fill="#dcfce7"/><text x="335" y="83" font-family="system-ui" font-size="6" fill="#16a34a">OK</text>
<text x="22" y="100" font-family="monospace" font-size="7" fill="#334155">/api/v1/search</text>
<text x="150" y="100" font-family="monospace" font-size="7" fill="#334155">32ms</text>
<text x="210" y="100" font-family="monospace" font-size="7" fill="#334155">89ms</text>
<text x="270" y="100" font-family="monospace" font-size="7" fill="#334155">210ms</text>
<rect x="330" y="92" width="24" height="12" rx="6" fill="#dcfce7"/><text x="335" y="101" font-family="system-ui" font-size="6" fill="#16a34a">OK</text>
<text x="22" y="118" font-family="monospace" font-size="7" fill="#334155">/api/v1/browse</text>
<text x="150" y="118" font-family="monospace" font-size="7" fill="#334155">28ms</text>
<text x="210" y="118" font-family="monospace" font-size="7" fill="#334155">65ms</text>
<text x="270" y="118" font-family="monospace" font-size="7" fill="#334155">150ms</text>
<rect x="330" y="110" width="24" height="12" rx="6" fill="#dcfce7"/><text x="335" y="119" font-family="system-ui" font-size="6" fill="#16a34a">OK</text>
<text x="22" y="136" font-family="monospace" font-size="7" fill="#334155">/api/v1/export</text>
<text x="150" y="136" font-family="monospace" font-size="7" fill="#334155">890ms</text>
<text x="210" y="136" font-family="monospace" font-size="7" fill="#f59e0b">2.1s</text>
<text x="270" y="136" font-family="monospace" font-size="7" fill="#ef4444">4.8s</text>
<rect x="330" y="128" width="30" height="12" rx="6" fill="#fef9c3"/><text x="333" y="137" font-family="system-ui" font-size="6" fill="#ca8a04">WARN</text>
</svg>`,
  // res-001 is the SQL Style Guide, a markdown resource. A managed resource's
  // tile is a capture exactly as an asset's is (#1554), and its own page now
  // shows that tile beside a Recapture control (#1568): without an entry here
  // both surfaces document a placeholder.
  "res-001": `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
<rect width="400" height="300" fill="white"/>
<text x="16" y="30" font-family="system-ui" font-size="15" font-weight="700" fill="#0f172a">SQL Style Guide</text>
<text x="16" y="50" font-family="system-ui" font-size="8" fill="#64748b">Formatting and naming conventions for all data teams</text>
<text x="16" y="76" font-family="system-ui" font-size="10" font-weight="600" fill="#1e293b">Naming</text>
<text x="16" y="92" font-family="system-ui" font-size="8" fill="#334155">Tables are plural and snake_case: daily_sales, store_inventory.</text>
<text x="16" y="106" font-family="system-ui" font-size="8" fill="#334155">A column that holds an identifier ends in _id, and nothing else does.</text>
<text x="16" y="130" font-family="system-ui" font-size="10" font-weight="600" fill="#1e293b">Common table expressions</text>
<rect x="16" y="138" width="368" height="58" rx="4" fill="#f1f5f9"/>
<text x="24" y="154" font-family="monospace" font-size="8" fill="#334155">WITH recent_orders AS (</text>
<text x="24" y="168" font-family="monospace" font-size="8" fill="#334155">  SELECT store_id, total FROM analytics.daily_sales</text>
<text x="24" y="182" font-family="monospace" font-size="8" fill="#334155">  WHERE order_date &gt;= CURRENT_DATE - INTERVAL '30' DAY</text>
<text x="16" y="216" font-family="system-ui" font-size="10" font-weight="600" fill="#1e293b">Join ordering</text>
<text x="16" y="232" font-family="system-ui" font-size="8" fill="#334155">Put the largest table first and let the planner reorder from there.</text>
<text x="16" y="246" font-family="system-ui" font-size="8" fill="#334155">Every join names its keys; a natural join is never used.</text>
<text x="16" y="270" font-family="system-ui" font-size="8" fill="#64748b">Aliases are the table's initials, lowercase, and never a single letter.</text>
</svg>`,
};
const portalShares: Record<string, Share[]> = JSON.parse(
  JSON.stringify(mockShares),
);
let shareCounter = 100;

// Mutable feedback-thread state for the mock server.
const portalThreads = JSON.parse(JSON.stringify(mockThreads)) as typeof mockThreads;
const portalThreadEvents: Record<string, typeof mockThreadEvents[string]> = JSON.parse(
  JSON.stringify(mockThreadEvents),
);
let threadCounter = 100;

// Mutable SMTP settings and notification preferences (#631) so saves reflect
// in subsequent reads within a single mock-server session. The password is
// never stored or returned; password_set mirrors the backend's write-only
// password semantics.
const smtpSettings = {
  enabled: true,
  host: "smtp.example.com",
  port: 587,
  username: "mailer@example.com",
  password_set: true,
  from: "platform@example.com",
  from_name: "Data Platform",
  tls_mode: "starttls",
  updated_by: "sarah.chen@example.com",
  updated_at: "2026-04-10T15:30:00Z",
  warnings: [] as string[],
};

// smtpWarnings mirrors the server's hazard check on the STORED settings
// (#1072): credentials plus tls_mode none means the username and password
// cross the wire in the clear.
function smtpWarnings(): string[] {
  if (smtpSettings.tls_mode !== "none") return [];
  if (smtpSettings.username === "" && !smtpSettings.password_set) return [];
  return [
    "TLS is disabled (tls_mode: none) while SMTP credentials are configured; " +
      "the username and password are sent in cleartext. Use starttls or implicit unless the relay is on a closed network.",
  ];
}

// reviewQueueAlert is the knowledge review-queue staleness alert settings
// (#803). The fixture is a configured alert so the settings screenshot shows
// the section doing its job rather than an empty form.
const reviewQueueAlert = {
  enabled: true,
  pending_threshold: 25,
  oldest_pending_days: 30,
  cooldown_hours: 24,
  recipients: ["sarah.chen@example.com"],
  updated_by: "sarah.chen@example.com",
  updated_at: "2026-07-28T09:15:00Z",
  warnings: [] as string[],
};

// reviewQueueAlertWarnings mirrors the server's check for a configuration that
// saves cleanly and delivers nothing.
function reviewQueueAlertWarnings(): string[] {
  if (!reviewQueueAlert.enabled) return [];
  const out: string[] = [];
  if (reviewQueueAlert.recipients.length === 0) {
    out.push(
      "no recipients are configured, so no alert will be delivered; add at least one address",
    );
  }
  if (reviewQueueAlert.pending_threshold <= 0 && reviewQueueAlert.oldest_pending_days <= 0) {
    out.push(
      "both thresholds are 0, so nothing can cross; set a pending count, an age in days, or both",
    );
  }
  return out;
}

// mockNotificationRows backs both delivery-history surfaces: the admin
// monitoring tab reads them whole, the user's own screen reads the subset a
// recipient sees. One fixture keeps the two screenshots telling one story.
const mockNotificationRows = [
  {
    id: 5121,
    recipient: "marcus.johnson@example.com",
    category: "share",
    subject: 'lisa.chang@example.com shared the asset "Q3 Revenue by Region" with you',
    digest: false,
    status: "failed",
    attempts: 5,
    last_error: "dial tcp 10.24.0.31:587: connect: connection refused",
    item_title: "Q3 Revenue by Region",
    actor: "lisa.chang@example.com",
    scheduled_for: "2026-07-29T14:02:00Z",
    created_at: "2026-07-29T14:01:00Z",
  },
  {
    id: 5120,
    recipient: "marcus.johnson@example.com",
    category: "mention",
    subject: 'lisa.chang@example.com mentioned you on "Warehouse Cost Review"',
    digest: false,
    status: "sent",
    attempts: 1,
    item_title: "Warehouse Cost Review",
    actor: "lisa.chang@example.com",
    link: "https://platform.example.com/portal/assets/ast-3",
    scheduled_for: "2026-07-29T09:15:00Z",
    sent_at: "2026-07-29T09:15:04Z",
    created_at: "2026-07-29T09:15:00Z",
  },
  {
    id: 5119,
    recipient: "marcus.johnson@example.com",
    category: "comment",
    subject: "3 updates in your daily digest",
    digest: true,
    status: "pending",
    attempts: 0,
    item_title: "Customer Churn Analysis",
    actor: "priya.patel@example.com",
    scheduled_for: "2026-07-30T13:00:00Z",
    created_at: "2026-07-29T16:40:00Z",
  },
];

const notificationPrefs = {
  mode: "immediate",
  shares_enabled: true,
  comments_enabled: true,
  mentions_enabled: true,
  // Server-computed from the stored SMTP settings; the mock SMTP section is
  // enabled with a host, so delivery is available here.
  delivery_available: true,
};

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

// tableRegisterResponse answers a register call with the registration it made,
// or with the RFC 9457 refusal of a CSV a query engine cannot read the way it
// is stored -- which is the state the portal offers to correct (#1441).
function tableRegisterResponse(
  result: Awaited<ReturnType<typeof mockRegisterTable>>,
): HttpResponse<TableRegistration | typeof tornCSVProblem> {
  if ("status" in result && result.status === tornCSVProblem.status) {
    return HttpResponse.json(result, {
      status: tornCSVProblem.status,
      headers: { "Content-Type": "application/problem+json" },
    });
  }
  return HttpResponse.json(result, { status: 201 });
}

// ---------------------------------------------------------------------------
// Handlers — combined admin + portal
// ---------------------------------------------------------------------------


/**
 * The bytes a mock resource serves.
 *
 * A captured fixture has its own body in `mockResourceContent`; everything
 * else falls back to something VALID FOR ITS TYPE. The fallback matters: the
 * viewer renders whatever it is handed, so a markdown placeholder served for a
 * `text/csv` resource becomes a one-column, one-row table, which is what the
 * committed docs screenshots used to show. Genuinely binary types keep a
 * placeholder, because nothing textual would render correctly for them either.
 */
function resourceBody(resource: { id: string; mime_type: string; filename: string; display_name: string; description: string }): string {
  const own = mockResources.content[resource.id];
  if (own !== undefined) return own;
  const type = resource.mime_type;
  if (type === "text/csv" || type === "text/tab-separated-values") {
    const sep = type === "text/csv" ? "," : "\t";
    return [
      ["name", "category", "value"].join(sep),
      ["First entry", "example", "1"].join(sep),
      ["Second entry", "example", "2"].join(sep),
      ["Third entry", "example", "3"].join(sep),
      "",
    ].join("\n");
  }
  if (type === "application/sql" || type === "text/x-sql") {
    return `-- ${resource.display_name}\n-- ${resource.description}\n\nSELECT 1;\n`;
  }
  if (type === "application/json") {
    return `${JSON.stringify({ name: resource.display_name, description: resource.description }, null, 2)}\n`;
  }
  if (type.startsWith("text/")) {
    return `# ${resource.display_name}\n\n${resource.description}\n`;
  }
  return `binary contents of ${resource.filename}`;
}

export const handlers = [
  // =========================================================================
  // Public (unauthenticated)
  // =========================================================================

  http.get(`${ADMIN_BASE}/public/branding`, () =>
    HttpResponse.json({
      name: mockSystemInfo.name,
      version: mockSystemInfo.version,
      portal_title: mockSystemInfo.portal_title,
      brand_name: mockSystemInfo.brand_name,
      brand_url: mockSystemInfo.brand_url,
      version_url: mockSystemInfo.version_url,
      oidc_button_label: "", // empty -> portal falls back to the default "Sign in with OIDC"
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
      user_id: "sarah.chen@example.com",
      email: "sarah.chen@example.com",
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
        "datahub_get_lineage",
        "datahub_browse",
        "s3_list",
        "s3_object",
        "memory_capture",
        "apply_knowledge",
        "save_asset",
        "manage_asset",
      ],
      csrf_token: "mock-csrf-token",
    }),
  ),

  // --- Knowledge pages (#633) ---
  http.get(`${PORTAL_BASE}/knowledge-pages`, ({ request }) => {
    const q = new URL(request.url).searchParams.get("q")?.toLowerCase() ?? "";
    const pages = mockKnowledgePages.filter(
      (p) => !p.deleted_at && (!q || p.title.toLowerCase().includes(q)),
    );
    return HttpResponse.json({ pages, total: pages.length });
  }),
  // Corpus-wide reference graph (#1162), the alternate layout to the cards view.
  http.get(`${PORTAL_BASE}/knowledge-pages/graph`, ({ request }) => {
    const sp = new URL(request.url).searchParams;
    return HttpResponse.json(
      mockKnowledgeGraph(sp.get("tag") ?? "", Number(sp.get("limit") ?? 0)),
    );
  }),
  http.get(`${PORTAL_BASE}/knowledge-pages/search`, ({ request }) => {
    const q = new URL(request.url).searchParams.get("q")?.toLowerCase() ?? "";
    const scored = mockKnowledgePages
      .filter((p) => !p.deleted_at && (p.title.toLowerCase().includes(q) || p.body.toLowerCase().includes(q)))
      .map((page) => ({ page, score: 0.9 }));
    return HttpResponse.json(scored);
  }),
  // Entity references (#664, #1159): what a page links to, the reverse lookup
  // an entity view reads, and the batch resolve the markdown renderer calls.
  // The backlinks and resolve routes are literal-segment matches, so they are
  // registered before the `:id` routes that would otherwise swallow them.
  http.get(`${PORTAL_BASE}/knowledge-pages/backlinks`, ({ request }) => {
    const urn = new URL(request.url).searchParams.get("urn") ?? "";
    return HttpResponse.json({ pages: pagesReferencing(urn) });
  }),
  http.post(`${PORTAL_BASE}/knowledge-pages/refs/resolve`, async ({ request }) => {
    const body = (await request.json()) as { urns?: string[] };
    const refs = (body.urns ?? []).map((urn) => ({
      ...resolvePageRef(urn, "manual"),
      accessible: true,
    }));
    return HttpResponse.json({ refs });
  }),
  http.get(`${PORTAL_BASE}/knowledge-pages/:id/refs`, ({ params }) =>
    HttpResponse.json({ refs: pageRefs(String(params.id)) }),
  ),
  http.put(`${PORTAL_BASE}/knowledge-pages/:id/refs`, async ({ params, request }) => {
    const body = (await request.json()) as { refs?: string[] };
    return HttpResponse.json({ refs: setPageRefs(String(params.id), body.refs ?? []) });
  }),
  http.get(`${PORTAL_BASE}/knowledge-pages/:id/versions`, ({ params }) => {
    const page = mockKnowledgePages.find((p) => p.id === params.id);
    const versions = page
      ? [
          {
            id: `${page.id}-v${page.current_version}`,
            page_id: page.id,
            version: page.current_version,
            title: page.title,
            summary: page.summary,
            body: page.body,
            tags: page.tags,
            created_by: page.updated_by,
            change_summary: "edit",
            created_at: page.updated_at,
          },
        ]
      : [];
    return HttpResponse.json({ versions, total: versions.length });
  }),
  http.get(`${PORTAL_BASE}/knowledge-pages/:id`, ({ params }) => {
    const page = mockKnowledgePages.find((p) => p.id === params.id && !p.deleted_at);
    return page ? HttpResponse.json(page) : new HttpResponse(null, { status: 404 });
  }),
  http.post(`${PORTAL_BASE}/knowledge-pages`, async ({ request }) => {
    const reqBody = (await request.json()) as Record<string, unknown>;
    const now = new Date().toISOString();
    const page = {
      id: `kp-${mockKnowledgePages.length + 1}`,
      title: String(reqBody.title ?? ""),
      summary: String(reqBody.summary ?? ""),
      body: String(reqBody.body ?? ""),
      tags: (reqBody.tags as string[]) ?? [],
      created_by: "sarah.chen@example.com",
      updated_by: "sarah.chen@example.com",
      current_version: 1,
      created_at: now,
      updated_at: now,
    };
    mockKnowledgePages.push(page);
    return HttpResponse.json(page, { status: 201 });
  }),
  // The way back from hiding a built-in page (#1390).
  http.post(`${PORTAL_BASE}/knowledge-pages/restore-builtin`, () => {
    let restored = 0;
    for (const p of mockKnowledgePages) {
      if (p.builtin && p.deleted_at) {
        delete p.deleted_at;
        restored += 1;
      }
    }
    return HttpResponse.json({ restored });
  }),
  http.put(`${PORTAL_BASE}/knowledge-pages/:id`, async ({ params, request }) => {
    const reqBody = (await request.json()) as Record<string, unknown>;
    const page = mockKnowledgePages.find((p) => p.id === params.id);
    if (!page) return new HttpResponse(null, { status: 404 });
    // Builtin pages are the platform's own documentation: read-only (#1390).
    if (page.builtin) {
      return HttpResponse.json(
        { error: "knowledge page is built-in platform documentation and read-only" },
        { status: 403 },
      );
    }
    page.title = String(reqBody.title ?? page.title);
    page.summary = String(reqBody.summary ?? page.summary);
    page.body = String(reqBody.body ?? page.body);
    page.tags = (reqBody.tags as string[]) ?? page.tags;
    page.current_version += 1;
    page.updated_at = new Date().toISOString();
    return HttpResponse.json(page);
  }),
  http.delete(`${PORTAL_BASE}/knowledge-pages/:id`, ({ params }) => {
    const page = mockKnowledgePages.find((p) => p.id === params.id);
    if (page) page.deleted_at = new Date().toISOString();
    return new HttpResponse(null, { status: 204 });
  }),

  // --- DataHub Catalog + Context Docs (#719/#720) ---
  http.get(`${PORTAL_BASE}/datahub/connections`, () =>
    HttpResponse.json({ connections: mockDataHubConnections }),
  ),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/browse`, () =>
    HttpResponse.json({ results: catalogBrowse() }),
  ),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/search`, ({ request }) => {
    const params = new URL(request.url).searchParams;
    const q = params.get("q") ?? "";
    // The backend accepts `tags` repeated or comma-separated; the Tags surface
    // sends one tag URN to list what carries it (#1156).
    const tags = params.getAll("tags").flatMap((v) => v.split(",")).filter(Boolean);
    return HttpResponse.json({
      results: catalogSearch(q, {
        tags,
        // The Domains surface sends one domain URN to list the tables in it
        // (#1157); the Glossary surface sends a term URN under one of the two
        // glossary filters to list what it is applied to (#1158).
        domain: params.get("domain") ?? "",
        glossaryTerm: params.get("glossary_term") ?? "",
        columnGlossaryTerm: params.get("column_glossary_term") ?? "",
      }),
    });
  }),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/entity`, ({ request }) => {
    const u = new URL(request.url).searchParams.get("urn") ?? "";
    const entity = catalogEntity(u);
    // A URN the catalog has never ingested is a 404 (#1610): DataHub reports it
    // through the entity's `exists` field, and the route separates that from
    // the 502 it answers for a catalog it could not reach. Answering 200 with
    // the URN echoed back, as this did while the platform read existence off
    // the record's own fields, would mock a response the backend no longer
    // produces and would hide the "cited but not in the catalog" path.
    if (!entity) {
      return HttpResponse.json({ detail: `datahub holds no entity for ${u}` }, { status: 404 });
    }
    return HttpResponse.json(entity);
  }),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/lookup/tags`, ({ request }) => {
    const q = new URL(request.url).searchParams.get("q") ?? "";
    return HttpResponse.json({ results: lookupTags(q) });
  }),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/lookup/glossary-terms`, ({ request }) => {
    const q = new URL(request.url).searchParams.get("q") ?? "";
    return HttpResponse.json({ results: lookupGlossaryTerms(q) });
  }),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/lookup/domains`, () =>
    HttpResponse.json({ results: lookupDomains() }),
  ),
  ...(["description", "tags", "owners", "glossary-terms", "domain"] as const).map((field) =>
    http.put(`${PORTAL_BASE}/datahub/:conn/catalog/entity/${field}`, async ({ request }) => {
      const body = (await request.json()) as Record<string, unknown>;
      if (!body.urn) return HttpResponse.json({ detail: "urn is required" }, { status: 400 });
      applyCatalogChange(field, body as never);
      return HttpResponse.json({ status: "ok" });
    }),
  ),
  // Tag governance (#1156): the list read is the picker's lookup route above.
  http.post(`${PORTAL_BASE}/datahub/:conn/catalog/tags`, async ({ request }) => {
    const body = (await request.json()) as { name?: string; description?: string };
    const name = (body.name ?? "").trim();
    if (!name) return HttpResponse.json({ detail: "name is required" }, { status: 400 });
    return HttpResponse.json({ urn: createTag(name, body.description) }, { status: 201 });
  }),
  http.delete(`${PORTAL_BASE}/datahub/:conn/catalog/tags`, ({ request }) => {
    const tagUrn = new URL(request.url).searchParams.get("urn") ?? "";
    if (!tagUrn.startsWith("urn:li:tag:") || tagUrn === "urn:li:tag:") {
      return HttpResponse.json({ detail: `invalid urn: ${tagUrn}` }, { status: 400 });
    }
    return deleteTag(tagUrn)
      ? HttpResponse.json({ status: "deleted" })
      : HttpResponse.json({ detail: "tag delete failed" }, { status: 502 });
  }),
  // Domain governance (#1157): the list read is the picker's lookup route above,
  // and the membership read is the catalog search's domain filter.
  http.post(`${PORTAL_BASE}/datahub/:conn/catalog/domains`, async ({ request }) => {
    const body = (await request.json()) as { name?: string; description?: string };
    const name = (body.name ?? "").trim();
    if (!name) return HttpResponse.json({ detail: "name is required" }, { status: 400 });
    return HttpResponse.json({ urn: createDomain(name, body.description) }, { status: 201 });
  }),
  http.delete(`${PORTAL_BASE}/datahub/:conn/catalog/domains`, ({ request }) => {
    const domainUrn = new URL(request.url).searchParams.get("urn") ?? "";
    if (!domainUrn.startsWith("urn:li:domain:") || domainUrn === "urn:li:domain:") {
      return HttpResponse.json({ detail: `invalid urn: ${domainUrn}` }, { status: 400 });
    }
    return deleteDomain(domainUrn)
      ? HttpResponse.json({ status: "deleted" })
      : HttpResponse.json({ detail: "domain delete failed" }, { status: 502 });
  }),
  // Glossary (#1155 hierarchy, #1158 browser and editor). The definition edit is
  // the entity-description route above, and a term's usage is the catalog
  // search's glossary filters, so neither is a route of its own.
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/glossary/roots`, () =>
    HttpResponse.json(glossaryRoots()),
  ),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/glossary/children`, ({ request }) => {
    const nodeUrn = new URL(request.url).searchParams.get("urn") ?? "";
    if (!nodeUrn.startsWith("urn:li:glossaryNode:")) {
      return HttpResponse.json({ detail: `invalid urn: ${nodeUrn}` }, { status: 400 });
    }
    const children = glossaryChildren(nodeUrn);
    return children
      ? HttpResponse.json(children)
      : HttpResponse.json({ detail: "glossary node not found" }, { status: 404 });
  }),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/glossary/term`, ({ request }) => {
    const termUrn = new URL(request.url).searchParams.get("urn") ?? "";
    if (!termUrn.startsWith("urn:li:glossaryTerm:")) {
      return HttpResponse.json({ detail: `invalid urn: ${termUrn}` }, { status: 400 });
    }
    const term = glossaryTerm(termUrn);
    return term
      ? HttpResponse.json(term)
      : HttpResponse.json({ detail: "glossary term not found" }, { status: 404 });
  }),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/glossary/parents`, ({ request }) => {
    const entityUrn = new URL(request.url).searchParams.get("urn") ?? "";
    if (!entityUrn.startsWith("urn:li:glossary")) {
      return HttpResponse.json({ detail: `invalid urn: ${entityUrn}` }, { status: 400 });
    }
    return HttpResponse.json({ parents: glossaryParents(entityUrn) });
  }),
  ...(["terms", "nodes"] as const).map((path) =>
    http.post(`${PORTAL_BASE}/datahub/:conn/catalog/glossary/${path}`, async ({ request }) => {
      const body = (await request.json()) as {
        name?: string;
        definition?: string;
        parent_node?: string;
      };
      const name = (body.name ?? "").trim();
      if (!name) return HttpResponse.json({ detail: "name is required" }, { status: 400 });
      const kind = path === "terms" ? "term" : "node";
      return HttpResponse.json(
        { urn: createGlossaryEntity(kind, name, body.definition, body.parent_node) },
        { status: 201 },
      );
    }),
  ),
  http.delete(`${PORTAL_BASE}/datahub/:conn/catalog/glossary/entity`, ({ request }) => {
    const entityUrn = new URL(request.url).searchParams.get("urn") ?? "";
    if (!entityUrn.startsWith("urn:li:glossaryTerm:") && !entityUrn.startsWith("urn:li:glossaryNode:")) {
      return HttpResponse.json({ detail: `invalid urn: ${entityUrn}` }, { status: 400 });
    }
    return deleteGlossaryEntity(entityUrn)
      ? HttpResponse.json({ status: "deleted" })
      : HttpResponse.json({ detail: "glossary entity delete failed" }, { status: 502 });
  }),
  http.get(`${PORTAL_BASE}/datahub/:conn/catalog/entity/documents`, ({ request }) => {
    const entityUrn = new URL(request.url).searchParams.get("urn") ?? "";
    if (!entityUrn) return HttpResponse.json({ detail: "urn is required" }, { status: 400 });
    return HttpResponse.json({ documents: entityDocuments(entityUrn) });
  }),
  http.get(`${PORTAL_BASE}/datahub/:conn/documents/browse`, () => HttpResponse.json(docsBrowse())),
  http.get(`${PORTAL_BASE}/datahub/:conn/documents/search`, ({ request }) => {
    const q = new URL(request.url).searchParams.get("q") ?? "";
    return HttpResponse.json({ documents: docsSearch(q) });
  }),
  http.get(`${PORTAL_BASE}/datahub/:conn/documents/:id`, ({ params }) => {
    const doc = getDoc(String(params.id));
    return doc ? HttpResponse.json(doc) : new HttpResponse(null, { status: 404 });
  }),
  http.post(`${PORTAL_BASE}/datahub/:conn/documents`, async ({ request }) => {
    const body = (await request.json()) as { entity_urn?: string; title?: string; content?: string; category?: string };
    if (!body.title) return HttpResponse.json({ detail: "title is required" }, { status: 400 });
    if (!body.entity_urn) return HttpResponse.json({ detail: "entity_urn is required" }, { status: 400 });
    return HttpResponse.json(createDoc({ ...body, title: body.title, content: body.content ?? "" }), { status: 201 });
  }),
  http.put(`${PORTAL_BASE}/datahub/:conn/documents/:id`, async ({ params, request }) => {
    const body = (await request.json()) as { title?: string; content?: string; category?: string };
    if (!body.title) return HttpResponse.json({ detail: "title is required" }, { status: 400 });
    const doc = updateDoc(String(params.id), { title: body.title, content: body.content ?? "", category: body.category });
    return doc ? HttpResponse.json(doc) : new HttpResponse(null, { status: 502 });
  }),
  http.delete(`${PORTAL_BASE}/datahub/:conn/documents/:id`, ({ params }) =>
    deleteDoc(String(params.id)) ? HttpResponse.json({ status: "deleted" }) : new HttpResponse(null, { status: 502 }),
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

  // Indexing dashboard: cross-kind summary, job drill-down, re-index.
  http.get(`${ADMIN_BASE}/index-jobs`, () => HttpResponse.json(mockIndexJobsSummary)),

  http.get(`${ADMIN_BASE}/index-jobs/jobs`, ({ request }) => {
    const url = new URL(request.url);
    const kind = url.searchParams.get("kind");
    const status = url.searchParams.get("status");
    let jobs = mockIndexJobs;
    if (kind) jobs = jobs.filter((j) => j.source_kind === kind);
    if (status) jobs = jobs.filter((j) => j.status === status);
    return HttpResponse.json({ jobs });
  }),

  http.get(`${ADMIN_BASE}/index-jobs/failures`, ({ request }) => {
    const url = new URL(request.url);
    const kind = url.searchParams.get("kind");
    let failures = mockIndexJobsFailures;
    if (kind) failures = failures.filter((f) => f.source_kind === kind);
    return HttpResponse.json({ failures });
  }),

  http.post(`${ADMIN_BASE}/index-jobs/reindex`, async ({ request }) => {
    const body = (await request.json().catch(() => ({}))) as {
      kind?: string;
      source_id?: string;
    };
    const enqueued = body.source_id ? [body.source_id] : ["all"];
    return HttpResponse.json(
      { status: "queued", enqueued, count: enqueued.length },
      { status: 202 },
    );
  }),

  http.post(`${ADMIN_BASE}/index-jobs/dismiss`, async ({ request }) => {
    await request.json().catch(() => ({}));
    return HttpResponse.json({ status: "resolved", resolved: 1 });
  }),

  http.get(`${ADMIN_BASE}/audit/events/filters`, () => {
    const users = [...new Set(mockAuditEvents.map((e) => e.user_id))].sort();
    const tools = [...new Set(mockAuditEvents.map((e) => e.tool_name))].sort();
    const toolkit_kinds = [
      ...new Set(mockAuditEvents.map((e) => e.toolkit_kind).filter(Boolean)),
    ].sort();
    const sources = [
      ...new Set(mockAuditEvents.map((e) => e.source).filter(Boolean)),
    ].sort();
    // The label map the real endpoint returns beside the facet: the address
    // each principal acts for. A script's principal resolves to its owner, so
    // several ids here answer with one address and the facet has to say which
    // principal it is offering (#1523).
    const user_labels: Record<string, string> = {};
    for (const e of mockAuditEvents) {
      if (e.user_email) user_labels[e.user_id] ??= e.user_email;
    }
    return HttpResponse.json({ users, tools, toolkit_kinds, sources, user_labels });
  }),

  http.get(`${ADMIN_BASE}/audit/events`, ({ request }) => {
    const url = new URL(request.url);
    const page = parseInt(url.searchParams.get("page") ?? "1", 10);
    const perPage = parseInt(url.searchParams.get("per_page") ?? "20", 10);
    const userId = url.searchParams.get("user_id");
    const toolName = url.searchParams.get("tool_name");
    const toolkitKind = url.searchParams.get("toolkit_kind");
    const sourceParam = url.searchParams.get("source");
    const success = url.searchParams.get("success");
    const search = url.searchParams.get("search")?.toLowerCase();
    const sortBy = url.searchParams.get("sort_by") as AuditSortColumn | null;
    const sortOrder = url.searchParams.get("sort_order") ?? "desc";

    let filtered = filterByTimeRange(url, mockAuditEvents);
    if (userId) filtered = filtered.filter((e) => e.user_id === userId);
    if (toolName) filtered = filtered.filter((e) => e.tool_name === toolName);
    if (toolkitKind) filtered = filtered.filter((e) => e.toolkit_kind === toolkitKind);
    if (sourceParam) filtered = filtered.filter((e) => e.source === sourceParam);
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
          (e.purpose ?? "").toLowerCase().includes(search) ||
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

  http.get(`${OBSERVABILITY_BASE}/query`, ({ request }) => {
    const url = new URL(request.url);
    return HttpResponse.json(promInstantFor(url.searchParams.get("query") ?? ""));
  }),

  http.get(`${OBSERVABILITY_BASE}/query_range`, ({ request }) => {
    const url = new URL(request.url);
    const query = url.searchParams.get("query") ?? "";
    const start = Number(url.searchParams.get("start") ?? "0");
    const end = Number(url.searchParams.get("end") ?? "0");
    const step = Number(url.searchParams.get("step") ?? "60");
    return HttpResponse.json(promRangeFor(query, start, end, step));
  }),

  http.get(`${ADMIN_BASE}/connection-instances/effective`, () => {
    return HttpResponse.json([
      {
        kind: "trino",
        name: "acme-warehouse",
        connection: "acme-warehouse",
        description:
          "Production data warehouse with retail, inventory, and analytics schemas",
        source: "file",
        file_declared: true,
        tools: [
          "trino_query",
          "trino_describe_table",
          "trino_browse",
          "trino_explain",
        ],
        config: { host: "trino.internal:8080", catalog: "warehouse" },
        updated_at: new Date(Date.now() - 14 * 86400000).toISOString(),
      },
      {
        kind: "trino",
        name: "acme-staging",
        connection: "acme-staging",
        description:
          "Staging environment for testing schema changes and ETL pipelines",
        source: "database",
        tools: ["trino_query", "trino_describe_table"],
        config: {
          host: "trino-staging.internal:8080",
          catalog: "warehouse",
        },
        created_by: "sarah.chen@example.com",
        updated_at: new Date(Date.now() - 7 * 86400000).toISOString(),
      },
      {
        kind: "datahub",
        name: "acme-catalog",
        connection: "acme-catalog",
        description:
          "Production metadata catalog with business glossary and data lineage",
        source: "file",
        file_declared: true,
        tools: [
          "datahub_search",
          "datahub_get_lineage",
          "datahub_browse",
        ],
        config: { url: "https://datahub.internal:8080" },
        updated_at: new Date(Date.now() - 21 * 86400000).toISOString(),
      },
      {
        kind: "datahub",
        name: "acme-catalog-staging",
        connection: "acme-catalog-staging",
        description:
          "Staging metadata catalog for testing ingestion recipes",
        source: "database",
        tools: ["datahub_search", "datahub_browse"],
        config: { url: "https://datahub-staging.internal:8080" },
        created_by: "marcus.johnson@example.com",
        updated_at: new Date(Date.now() - 3 * 86400000).toISOString(),
      },
      {
        kind: "s3",
        name: "acme-data-lake",
        connection: "acme-data-lake",
        description:
          "Raw data lake containing ETL outputs, CDC streams, and ML training data",
        source: "file",
        file_declared: true,
        tools: ["s3_list", "s3_object"],
        config: { region: "us-west-2", bucket: "acme-data-lake-prod" },
        updated_at: new Date(Date.now() - 30 * 86400000).toISOString(),
      },
      {
        kind: "s3",
        name: "acme-reports",
        connection: "acme-reports",
        description:
          "Generated reports and exported dashboards for stakeholder distribution",
        source: "file",
        file_declared: true,
        tools: ["s3_list", "s3_object"],
        config: { region: "us-west-2", bucket: "acme-reports-prod" },
        updated_at: new Date(Date.now() - 10 * 86400000).toISOString(),
      },
      {
        kind: "mcp",
        name: "acme-crm-gateway",
        connection: "acme-crm-gateway",
        description:
          "Gateway-proxied CRM MCP server. Responses are auto-enriched with DataHub context and Trino query availability.",
        source: "database",
        tools: [
          "crm_search_accounts",
          "crm_get_account",
          "crm_list_opportunities",
        ],
        config: { url: "https://crm-mcp.internal:9000", auth: "oauth2" },
        created_by: "sarah.chen@example.com",
        updated_at: new Date(Date.now() - 2 * 86400000).toISOString(),
        health: {
          reachable: true,
          last_success: new Date(Date.now() - 90 * 1000).toISOString(),
          last_error: "",
        },
      },
    ]);
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
    const order = url.searchParams.get("order");

    let filtered = [...mockInsights];
    if (status) filtered = filtered.filter((i) => i.status === status);
    if (category) filtered = filtered.filter((i) => i.category === category);
    if (confidence)
      filtered = filtered.filter((i) => i.confidence === confidence);
    if (entityUrn)
      filtered = filtered.filter((i) => i.entity_urns.includes(entityUrn));
    if (capturedBy)
      filtered = filtered.filter((i) => i.captured_by === capturedBy);
    filtered.sort((a, b) =>
      order === "oldest"
        ? a.created_at.localeCompare(b.created_at)
        : b.created_at.localeCompare(a.created_at),
    );

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

  http.get(`${ADMIN_BASE}/api-route-connections`, () =>
    HttpResponse.json(mockAPIRouteConnections()),
  ),

  http.post(`${ADMIN_BASE}/personas`, async ({ request }) => {
    const body = (await request.json()) as {
      name: string;
      display_name: string;
      description?: string;
      roles: string[];
      allow_tools: string[];
      deny_tools?: string[];
      api_routes?: APIRouteRule[];
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
      api_routes: body.api_routes ?? [],
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
      api_routes?: APIRouteRule[];
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
    // Assigned unconditionally: an absent api_routes means the persona has
    // none, which is exactly what a save that cleared every rule sends.
    existing.api_routes = body.api_routes ?? [];
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
          (a.description ?? "").toLowerCase().includes(search) ||
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
    return new HttpResponse(rewriteRefs(id, body), {
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
    // max_versions is tri-state on the wire (#1421): absent leaves the override
    // alone, null clears it, a number sets it.
    if (body.max_versions !== undefined) {
      portalAssets[idx]!.max_versions =
        body.max_versions === null ? undefined : (body.max_versions as number);
    }
    portalAssets[idx]!.updated_at = new Date().toISOString();
    return HttpResponse.json(portalAssets[idx]);
  }),

  http.put(`${ADMIN_BASE}/assets/:id/thumbnail`, async ({ params, request }) => {
    const asset = portalAssets.find((a) => a.id === params.id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const buffer = await request.arrayBuffer();
    thumbnailStore.set(asset.id, buffer);
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

  // Admin collections (#1292): no owner filter, so the agent-owned collection
  // the portal list cannot see is served here.
  http.get(`${ADMIN_BASE}/collections`, ({ request }) => {
    const url = new URL(request.url);
    const search = url.searchParams.get("search")?.toLowerCase();
    const limit = parseInt(url.searchParams.get("limit") ?? "50", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    let filtered = mockAllCollections.filter((c) => !c.deleted_at);
    if (search) {
      filtered = filtered.filter(
        (c) =>
          c.name.toLowerCase().includes(search) ||
          c.description.toLowerCase().includes(search) ||
          c.owner_email.toLowerCase().includes(search),
      );
    }

    return HttpResponse.json({
      data: filtered.slice(offset, offset + limit),
      total: filtered.length,
      limit,
      offset,
      share_summaries: { "col-001": { has_user_share: true, has_public_link: false } },
    });
  }),

  http.get(`${ADMIN_BASE}/collections/:id`, ({ params }) => {
    const coll = mockAllCollections.find((c) => c.id === params.id);
    if (!coll) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    if (coll.deleted_at) {
      return HttpResponse.json({ detail: "Gone" }, { status: 410 });
    }
    return HttpResponse.json(withItemAssetFields(coll));
  }),

  http.put(`${ADMIN_BASE}/collections/:id`, async ({ params, request }) => {
    const coll = mockAllCollections.find((c) => c.id === params.id && !c.deleted_at);
    if (!coll) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = (await request.json()) as Record<string, unknown>;
    if (body.name !== undefined) coll.name = body.name as string;
    if (body.description !== undefined) coll.description = body.description as string;
    coll.updated_at = new Date().toISOString();
    return HttpResponse.json(coll);
  }),

  http.delete(`${ADMIN_BASE}/collections/:id`, ({ params }) => {
    const coll = mockAllCollections.find((c) => c.id === params.id && !c.deleted_at);
    if (!coll) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    coll.deleted_at = new Date().toISOString();
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

  // Aggregated per-tool detail for the Tools master-detail page. Registered
  // AFTER /tools/schemas and /tools/call so the literal routes win the match.
  http.get(`${ADMIN_BASE}/tools/:name`, ({ params }) => {
    const name = decodeURIComponent(String(params["name"]));
    const detail = buildToolDetail(name);
    if (!detail) {
      return HttpResponse.json({ error: "tool not found" }, { status: 404 });
    }
    return HttpResponse.json(detail);
  }),

  // Cross-enrichment enrichment rules for a gateway-proxied connection.
  http.get(
    `${ADMIN_BASE}/gateway/connections/:connection/enrichment-rules`,
    ({ params }) => {
      const connection = decodeURIComponent(String(params["connection"]));
      return HttpResponse.json(mockEnrichmentRules[connection] ?? []);
    },
  ),

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

  http.post(`${PORTAL_BASE}/assets`, async ({ request }) => {
    const body = (await request.json()) as {
      name?: string;
      description?: string;
      content_type?: string;
      content?: string;
      tags?: string[];
    };
    if (!body.name?.trim()) {
      return HttpResponse.json({ detail: "name is required" }, { status: 400 });
    }
    if (!body.content_type?.trim()) {
      return HttpResponse.json({ detail: "content_type is required" }, { status: 400 });
    }
    if (!body.content) {
      return HttpResponse.json({ detail: "content is required" }, { status: 400 });
    }
    const id = `ast-${Date.now().toString(36)}`;
    const now = new Date().toISOString();
    const newAsset = {
      id,
      owner_id: "user-1",
      owner_email: "you@example.com",
      name: body.name.trim(),
      description: (body.description ?? "").trim(),
      content_type: body.content_type.trim(),
      s3_bucket: "mock-bucket",
      s3_key: `portal/user-1/${id}/content`,
      size_bytes: body.content.length,
      tags: body.tags ?? [],
      provenance: { tool_calls: [] },
      session_id: "",
      current_version: 1,
      thumbnail_version: 0,
      thumbnail_dark_version: 0,
      created_at: now,
      updated_at: now,
    };
    portalAssets.unshift(newAsset);
    return HttpResponse.json(newAsset, { status: 201 });
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

  // Content is served with its declared references rewritten to the URLs they
  // are served under, which is what the real route does and what makes a
  // referenced logo or data file load in a viewer and in a capture (#1497).
  http.get(`${PORTAL_BASE}/assets/:id/content`, ({ params }) => {
    const id = params.id as string;
    const asset = portalAssets.find((a) => a.id === id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = mockContent[id] ?? `[Mock content for ${asset.name}]`;
    return new HttpResponse(rewriteRefs(id, body), {
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
    // Every write records a version and moves the head, which is what leaves
    // the recorded thumbnail a version behind the body it shows (#1431).
    portalAssets[idx]!.current_version += 1;
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
    // max_versions is tri-state on the wire (#1421): absent leaves the override
    // alone, null clears it, a number sets it.
    if (body.max_versions !== undefined) {
      portalAssets[idx]!.max_versions =
        body.max_versions === null ? undefined : (body.max_versions as number);
    }
    portalAssets[idx]!.updated_at = new Date().toISOString();
    return HttpResponse.json(portalAssets[idx]);
  }),

  http.put(`${PORTAL_BASE}/assets/:id/thumbnail`, async ({ params, request }) => {
    const asset = portalAssets.find((a) => a.id === params.id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const url = new URL(request.url);
    const variant = url.searchParams.get("variant");
    // The capture is dated to the version it was rendered from, defaulting to
    // the version the asset is on now, exactly as the server does. Without the
    // stamp the mock would leave every captured asset pending and the queue
    // would re-offer it on every poll.
    const version = Number(url.searchParams.get("version") ?? asset.current_version);
    const buffer = await request.arrayBuffer();
    if (variant === "dark") {
      thumbnailStore.set(`${asset.id}:dark`, buffer);
      asset.thumbnail_dark_s3_key = `thumbnails/${asset.id}_dark.png`;
      asset.thumbnail_dark_version = version;
    } else {
      thumbnailStore.set(asset.id, buffer);
      asset.thumbnail_s3_key = `thumbnails/${asset.id}.png`;
      asset.thumbnail_version = version;
    }
    return new HttpResponse(null, { status: 204 });
  }),

  // Discarding an asset's captures is what returns it to the queue above: a
  // reader whose tile shows the artifact's error state has no other way to move
  // the row a capture is decided from (#1497).
  http.delete(`${PORTAL_BASE}/assets/:id/thumbnail`, ({ params }) => {
    const asset = portalAssets.find((a) => a.id === params.id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    thumbnailStore.delete(asset.id);
    thumbnailStore.delete(`${asset.id}:dark`);
    asset.thumbnail_s3_key = "";
    asset.thumbnail_dark_s3_key = "";
    asset.thumbnail_version = 0;
    asset.thumbnail_dark_version = 0;
    return HttpResponse.json({ status: "updated" });
  }),

  // The refresh queue's work list: the assets whose capture is missing or
  // behind the version they now hold. The server derives it in SQL over every
  // asset the caller owns; the mock asks the same question of the fixtures.
  http.get(`${PORTAL_BASE}/thumbnails/pending`, ({ request }) => {
    const limit = parseInt(new URL(request.url).searchParams.get("limit") ?? "25", 10);
    const pending = portalAssets.filter(
      (a) =>
        !a.deleted_at &&
        isThumbnailSupported(a.content_type) &&
        a.size_bytes <= THUMBNAIL_SOURCE_LIMIT &&
        thumbnailBehind(a),
    );
    return HttpResponse.json({
      data: pending.slice(0, limit),
      total: pending.length,
      limit,
      offset: 0,
    });
  }),

  http.get(`${PORTAL_BASE}/assets/:id/thumbnail`, ({ params, request }) => {
    const id = params.id as string;
    const variant = new URL(request.url).searchParams.get("variant");
    const served = serveThumbnail(id, variant);
    if (served) return served;
    const asset = portalAssets.find((a) => a.id === id);
    if (asset?.content_type.includes("svg") && mockContent[id]) {
      return new HttpResponse(mockContent[id], { headers: { "Content-Type": "image/svg+xml" } });
    }
    return new HttpResponse(null, { status: 404 });
  }),

  // The admin route answers the same variant parameter as the portal one: it is
  // where a collection's tiles come from when an administrator reads a
  // collection someone else owns (#1292), and that reader has a color mode too.
  http.get(`${ADMIN_BASE}/assets/:id/thumbnail`, ({ params, request }) => {
    const id = params.id as string;
    const variant = new URL(request.url).searchParams.get("variant");
    return serveThumbnail(id, variant) ?? new HttpResponse(null, { status: 404 });
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
        access_mode:
          (body.access_mode as Share["access_mode"]) ??
          (body.shared_with_user_id || body.shared_with_email ? "restricted" : "authenticated"),
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

  // --- Feedback threads (#601) ---
  // /threads/counts is registered before /threads/:id so the static segment
  // wins over the param.
  http.get(`${PORTAL_BASE}/threads/counts`, ({ request }) => {
    const url = new URL(request.url);
    const targetType = url.searchParams.get("target_type");
    const ids = (url.searchParams.get("ids") ?? "").split(",").filter(Boolean);
    const counts: Record<string, number> = {};
    for (const t of portalThreads) {
      if (t.deleted_at || t.status !== "open") continue;
      const tid =
        targetType === "collection"
          ? t.collection_id
          : targetType === "knowledge_page"
            ? t.knowledge_page_id
            : t.asset_id;
      if (tid && ids.includes(tid)) counts[tid] = (counts[tid] ?? 0) + 1;
    }
    return HttpResponse.json(counts);
  }),

  http.get(`${PORTAL_BASE}/threads`, ({ request }) => {
    const url = new URL(request.url);
    const q = url.searchParams;
    const matches = portalThreads.filter((t) => {
      if (t.deleted_at) return false;
      if (q.get("target_type") === "standalone") return t.target_type === "standalone";
      if (q.get("asset_id")) return t.asset_id === q.get("asset_id");
      if (q.get("collection_id")) return t.collection_id === q.get("collection_id");
      if (q.get("prompt_id")) return t.prompt_id === q.get("prompt_id");
      if (q.get("knowledge_page_id"))
        return t.knowledge_page_id === q.get("knowledge_page_id");
      return false;
    });
    const status = q.get("status");
    const kind = q.get("kind");
    const filtered = matches.filter(
      (t) => (!status || t.status === status) && (!kind || t.kind === kind),
    );
    return HttpResponse.json({
      data: filtered,
      total: filtered.length,
      limit: 50,
      offset: 0,
    });
  }),

  http.post(`${PORTAL_BASE}/threads`, async ({ request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    threadCounter++;
    const id = `thr-mock-${threadCounter}`;
    const now = new Date().toISOString();
    const thread = {
      id,
      kind: body.kind,
      target_type: body.target_type,
      asset_id: body.asset_id,
      collection_id: body.collection_id,
      prompt_id: body.prompt_id,
      knowledge_page_id: body.knowledge_page_id,
      anchor: body.anchor,
      target_version: body.target_version,
      title: body.title,
      author_id: "sarah.chen@example.com",
      author_email: "sarah.chen@example.com",
      status: "open",
      requires_resolution: body.requires_resolution === true,
      validation_state: "none",
      created_at: now,
      updated_at: now,
      event_count: 1,
      last_event_at: now,
      last_event_type: "comment",
    } as (typeof portalThreads)[number];
    portalThreads.unshift(thread);
    portalThreadEvents[id] = [
      {
        id: `evt-${id}-1`,
        thread_id: id,
        event_type: "comment",
        author_id: "sarah.chen@example.com",
        author_email: "sarah.chen@example.com",
        body: body.body as string,
        rating: body.rating as number | undefined,
        created_at: now,
      },
    ];
    return HttpResponse.json(thread, { status: 201 });
  }),

  // Capture a feedback thread as a reviewable insight (#662): mark the thread
  // resolved + linked, append an insight_linked event, and report the new id.
  http.post(`${PORTAL_BASE}/threads/:id/insight`, async ({ params, request }) => {
    const id = params.id as string;
    const thread = portalThreads.find((t) => t.id === id && !t.deleted_at);
    if (!thread) return HttpResponse.json({ error: "not found" }, { status: 404 });
    const body = (await request.json().catch(() => ({}))) as Record<string, unknown>;
    const insightId = `mem-mock-${++threadCounter}`;
    thread.insight_id = insightId;
    thread.status = "resolved";
    thread.updated_at = new Date().toISOString();
    (portalThreadEvents[id] ??= []).push({
      id: `evt-${id}-insight`,
      thread_id: id,
      event_type: "insight_linked",
      author_id: "sarah.chen@example.com",
      author_email: "sarah.chen@example.com",
      metadata: { insight_id: insightId },
      created_at: thread.updated_at,
    });
    return HttpResponse.json(
      { insight_id: insightId, status: "pending", linked: true, sink_class: body.sink_class ?? "business_knowledge" },
      { status: 201 },
    );
  }),

  http.get(`${PORTAL_BASE}/threads/:id/events`, ({ params }) =>
    HttpResponse.json({ data: portalThreadEvents[params.id as string] ?? [] }),
  ),

  // Resolved knowledge chain for a thread (#602). Registered before
  // /threads/:id so the static /chain segment is not swallowed.
  http.get(`${PORTAL_BASE}/threads/:id/chain`, ({ params }) => {
    const id = params.id as string;
    return HttpResponse.json(
      mockThreadChains[id] ?? { thread_id: id, insight_id: "", changesets: [] },
    );
  }),

  // Worklists / inbox (#603).
  http.get(`${PORTAL_BASE}/worklist/practitioner`, () => {
    const data = portalThreads.filter((t) => t.status === "open" && t.requires_resolution && !t.deleted_at);
    return HttpResponse.json({ data, total: data.length, limit: 50, offset: 0 });
  }),
  http.get(`${PORTAL_BASE}/worklist/sme`, () => {
    const data = portalThreads.filter((t) => t.validation_state === "pending" && !t.deleted_at);
    return HttpResponse.json({ data, total: data.length, limit: 50, offset: 0 });
  }),

  // Mentions inbox (#627): threads whose timeline named the caller.
  http.get(`${PORTAL_BASE}/worklist/mentions`, () => {
    const data = portalThreads.filter((t) => mentionedThreadIDs.has(t.id) && !t.deleted_at);
    return HttpResponse.json({ data, total: data.length, limit: 50, offset: 0 });
  }),

  // Feedback activity feed (#617): threads across the caller's assets,
  // collections, and prompts (not standalone), most recent first, each row
  // enriched with the target's display label so the feed can link back.
  http.get(`${PORTAL_BASE}/feedback/activity`, () => {
    const labels: Record<string, string> = {
      "ast-001": "Q4 Revenue Dashboard",
      "col-001": "Q4 Performance Review",
    };
    const data = portalThreads
      .filter((t) => !t.deleted_at && t.target_type !== "standalone")
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
      .map((t) => ({
        ...t,
        target_label:
          labels[t.asset_id ?? t.collection_id ?? t.prompt_id ?? ""] ??
          (t.target_type === "asset" ? "Asset" : t.target_type === "collection" ? "Collection" : "Prompt"),
      }));
    return HttpResponse.json({ data, total: data.length, limit: 50, offset: 0 });
  }),

  // Asset version history. Backed by data/assetVersions so the viewer's
  // version dropdown + revert affordance render as used (empty history is
  // still returned for assets with no recorded versions). Mocked so the
  // viewer never falls through to the real backend, which 401s and would
  // trip the global session-expiry logout in apiFetch mid-test.
  http.get(`${PORTAL_BASE}/assets/:id/versions`, ({ params }) => {
    const data = versionsForAsset(params.id as string);
    return HttpResponse.json({ data, total: data.length, limit: 50, offset: 0 });
  }),

  // Content of a specific asset version (viewing an older version read-only).
  http.get(`${PORTAL_BASE}/assets/:id/versions/:version/content`, ({ params }) => {
    const versions = versionsForAsset(params.id as string);
    const v = versions.find((x) => String(x.version) === String(params.version));
    const summary = v?.change_summary ?? "version";
    return HttpResponse.text(
      `<!-- v${params.version}: ${summary} -->\n<h1>Q4 Revenue Dashboard (v${params.version})</h1>`,
      { headers: { "Content-Type": "text/html" } },
    );
  }),

  // Sign-off aggregation (#603).
  http.get(`${PORTAL_BASE}/assets/:id/signoff`, () => HttpResponse.json({ signed_off: 1, stakeholders: 3 })),
  http.get(`${PORTAL_BASE}/collections/:id/signoff`, () => HttpResponse.json({ signed_off: 2, stakeholders: 2 })),

  // Validation response (#603): the author validates/disputes; dispute re-opens.
  http.post(`${PORTAL_BASE}/threads/:id/validation`, async ({ params, request }) => {
    const id = params.id as string;
    const body = (await request.json()) as { result: string; reason?: string };
    const thread = portalThreads.find((t) => t.id === id);
    if (!thread) return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    thread.validation_state = body.result as typeof thread.validation_state;
    if (body.result === "disputed") thread.status = "open";
    thread.updated_at = new Date().toISOString();
    return HttpResponse.json(thread);
  }),

  http.post(`${PORTAL_BASE}/threads/:id/events`, async ({ params, request }) => {
    const id = params.id as string;
    const body = (await request.json()) as Record<string, unknown>;
    const now = new Date().toISOString();
    const evt = {
      id: `evt-${id}-${(portalThreadEvents[id]?.length ?? 0) + 1}`,
      thread_id: id,
      event_type: (body.event_type as string) ?? "comment",
      author_id: "sarah.chen@example.com",
      author_email: "sarah.chen@example.com",
      body: body.body as string | undefined,
      rating: body.rating as number | undefined,
      created_at: now,
    } as (typeof portalThreadEvents)[string][number];
    portalThreadEvents[id] = [...(portalThreadEvents[id] ?? []), evt];
    const thread = portalThreads.find((t) => t.id === id);
    if (thread) {
      thread.event_count += 1;
      thread.last_event_at = now;
      thread.updated_at = now;
    }
    return HttpResponse.json(evt, { status: 201 });
  }),

  http.get(`${PORTAL_BASE}/threads/:id`, ({ params }) => {
    const thread = portalThreads.find((t) => t.id === params.id && !t.deleted_at);
    if (!thread) return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    return HttpResponse.json(thread);
  }),

  http.patch(`${PORTAL_BASE}/threads/:id`, async ({ params, request }) => {
    const thread = portalThreads.find((t) => t.id === params.id);
    if (!thread) return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    const body = (await request.json()) as Record<string, unknown>;
    if (typeof body.status === "string") thread.status = body.status as typeof thread.status;
    if (typeof body.requires_resolution === "boolean") thread.requires_resolution = body.requires_resolution;
    thread.updated_at = new Date().toISOString();
    return HttpResponse.json(thread);
  }),

  http.delete(`${PORTAL_BASE}/threads/:id`, ({ params }) => {
    const thread = portalThreads.find((t) => t.id === params.id);
    if (!thread) return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    thread.deleted_at = new Date().toISOString();
    return HttpResponse.json({ status: "deleted" });
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
      mockAuditEvents.filter((e) => e.user_id === "sarah.chen@example.com"),
    );
    return HttpResponse.json(computeOverview(userEvents));
  }),

  http.get(`${PORTAL_BASE}/activity/timeseries`, ({ request }) => {
    const url = new URL(request.url);
    const userEvents = filterByTimeRange(
      url,
      mockAuditEvents.filter((e) => e.user_id === "sarah.chen@example.com"),
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
      mockAuditEvents.filter((e) => e.user_id === "sarah.chen@example.com"),
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
      (i) => i.captured_by === "sarah.chen@example.com",
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
      (i) => i.captured_by === "sarah.chen@example.com",
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

  // =========================================================================
  // Portal — Collections
  // =========================================================================

  http.get(`${PORTAL_BASE}/collections`, ({ request }) => {
    const url = new URL(request.url);
    const search = url.searchParams.get("search");
    const limit = parseInt(url.searchParams.get("limit") ?? "50", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    let filtered = [...mockCollections];
    if (search) {
      const q = search.toLowerCase();
      filtered = filtered.filter(
        (c) =>
          c.name.toLowerCase().includes(q) ||
          c.description.toLowerCase().includes(q),
      );
    }

    const page = filtered.slice(offset, offset + limit);
    return HttpResponse.json({
      data: page,
      total: filtered.length,
      limit,
      offset,
      share_summaries: {},
    });
  }),

  http.get(`${PORTAL_BASE}/collections/:id/thumbnail`, ({ params }) => {
    const id = params.id as string;
    const buffer = thumbnailStore.get(`col-${id}`);
    if (buffer) {
      return new HttpResponse(buffer, {
        headers: { "Content-Type": "image/png" },
      });
    }
    // A fixture that declares a thumbnail already has one, generated by its
    // owner before it was shared. Serve its first item's capture rather than
    // 404ing, or a collection nobody can sign in as shows a placeholder next
    // to collections that have mosaics.
    // Shared collections live in their own fixture list, not in
    // mockAllCollections, and a shared one is exactly the case this
    // fallback exists for.
    const coll =
      mockAllCollections.find((c) => c.id === id) ??
      mockSharedCollections.find((sc) => sc.collection.id === id)?.collection;
    if (coll?.thumbnail_s3_key) {
      // The first item whose asset RESOLVES, not simply the first item: a
      // collection can lead with an asset that has no capture, and serving
      // that 404 turns the card's placeholder into a broken image, which is
      // worse than the placeholder it replaced.
      for (const item of (coll.sections ?? []).flatMap((sec) => sec.items ?? [])) {
        const served = serveThumbnail(item.asset_id, null);
        if (served) return served;
      }
    }
    return new HttpResponse(null, { status: 404 });
  }),

  http.put(`${PORTAL_BASE}/collections/:id/thumbnail`, async ({ params, request }) => {
    const id = params.id as string;
    const buffer = await request.arrayBuffer();
    thumbnailStore.set(`col-${id}`, buffer);
    const col = mockCollections.find((c) => c.id === id);
    if (col) (col as unknown as Record<string, unknown>).thumbnail_s3_key = `thumbnails/col-${id}.png`;
    return new HttpResponse(null, { status: 204 });
  }),

  http.get(`${PORTAL_BASE}/collections/:id`, ({ params }) => {
    const col = mockCollections.find((c) => c.id === params.id);
    if (!col) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    return HttpResponse.json({
      ...withItemAssetFields(col),
      is_owner: true,
      can_edit: true,
      can_manage: true,
    });
  }),

  http.post(`${PORTAL_BASE}/collections`, async ({ request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    const col = {
      ...mockCollections[0]!,
      id: `col-mock-${Date.now()}`,
      name: (body.name as string) ?? "New Collection",
      description: (body.description as string) ?? "",
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    return HttpResponse.json(col, { status: 201 });
  }),

  http.put(`${PORTAL_BASE}/collections/:id`, async ({ params, request }) => {
    const col = mockCollections.find((c) => c.id === params.id);
    if (!col) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = (await request.json()) as Record<string, unknown>;
    const updated = {
      ...col,
      name: (body.name as string) ?? col.name,
      description: (body.description as string) ?? col.description,
      updated_at: new Date().toISOString(),
    };
    return HttpResponse.json(updated);
  }),

  http.delete(`${PORTAL_BASE}/collections/:id`, () => {
    return new HttpResponse(null, { status: 204 });
  }),

  http.put(`${PORTAL_BASE}/collections/:id/sections`, ({ params }) => {
    const col = mockCollections.find((c) => c.id === params.id);
    if (!col) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    return HttpResponse.json({ ...col, updated_at: new Date().toISOString() });
  }),

  http.put(`${PORTAL_BASE}/collections/:id/config`, ({ params }) => {
    const col = mockCollections.find((c) => c.id === params.id);
    if (!col) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    return HttpResponse.json({ ...col, updated_at: new Date().toISOString() });
  }),

  http.get(
    `${PORTAL_BASE}/collections/:collectionId/shares`,
    ({ params }) => {
      const collectionId = params.collectionId as string;
      // Populate the primary demo collection so the share dialog reads as
      // used (active link + one expired); other collections have none.
      if (collectionId !== "col-001") return HttpResponse.json([]);
      const shares: Share[] = [
        {
          id: "shr-col-001",
          asset_id: "col-001",
          token: "tok_col_1_h7k2m9q4",
          created_by: "user-alice",
          shared_with_email: "david.park@example.com",
          permission: "viewer",
          access_mode: "restricted",
          expires_at: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
          revoked: false,
          access_count: 8,
          last_accessed_at: new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString(),
          created_at: "2025-01-06T15:10:00Z",
        },
        {
          id: "shr-col-002",
          asset_id: "col-001",
          token: "tok_col_2_p3w8z1x6",
          created_by: "user-alice",
          permission: "viewer",
          access_mode: "public",
          revoked: false,
          access_count: 23,
          last_accessed_at: "2025-01-08T11:02:00Z",
          created_at: "2025-01-03T09:30:00Z",
        },
      ];
      return HttpResponse.json(shares);
    },
  ),

  http.post(
    `${PORTAL_BASE}/collections/:collectionId/shares`,
    async ({ params, request }) => {
      const body = (await request.json()) as Record<string, unknown>;
      shareCounter++;
      const token = `tok_col_${shareCounter}_${Math.random().toString(36).slice(2, 10)}`;
      const share: Share = {
        id: `shr-col-${shareCounter}`,
        asset_id: params.collectionId as string,
        token,
        created_by: "user-alice",
        shared_with_user_id: body.shared_with_user_id as string | undefined,
        permission: (body.permission as Share["permission"]) ?? "viewer",
        access_mode:
          (body.access_mode as Share["access_mode"]) ??
          (body.shared_with_user_id || body.shared_with_email ? "restricted" : "authenticated"),
        expires_at: body.expires_in
          ? new Date(
              Date.now() + parseDuration(body.expires_in as string),
            ).toISOString()
          : undefined,
        revoked: false,
        access_count: 0,
        created_at: new Date().toISOString(),
        hide_expiration: body.hide_expiration === true,
        notice_text:
          typeof body.notice_text === "string" ? body.notice_text : undefined,
      };
      return HttpResponse.json({ share });
    },
  ),

  http.get(`${PORTAL_BASE}/shared-collections`, ({ request }) => {
    const url = new URL(request.url);
    const limit = parseInt(url.searchParams.get("limit") ?? "50", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    const page = mockSharedCollections.slice(offset, offset + limit);
    return HttpResponse.json({
      data: page,
      total: mockSharedCollections.length,
      limit,
      offset,
    });
  }),

  // =========================================================================
  // Resources (shared — /api/v1/resources)
  // =========================================================================

  // What a library holds, for the controls that narrow it: its folders with
  // exact counts, and every tag its resources carry (#1555). Registered ahead
  // of the by-id read, which would otherwise take "facets" for a resource id
  // and answer 404 -- which is what it did, so a library's folder rows never
  // rendered under the mocks and the two screenshots that open a folder could
  // not be produced at all.
  //
  // A folder counts everything beneath it at every depth, which is the lateral
  // expansion the store does: each resource contributes to every prefix of its
  // own path, so `data` counts what is filed at `data/weekly` too.
  http.get("/api/v1/resources/facets", ({ request }) => {
    const url = new URL(request.url);
    const scope = url.searchParams.get("scope");
    const scopeId = url.searchParams.get("scope_id");

    let visible = [...mockResources.resources];
    if (scope) visible = visible.filter((r) => r.scope === scope);
    if (scopeId) visible = visible.filter((r) => r.scope_id === scopeId);

    const counts = new Map<string, number>();
    const tags = new Set<string>();
    for (const r of visible) {
      const segments = r.path.split("/").filter(Boolean);
      for (let i = 1; i <= segments.length; i++) {
        const prefix = segments.slice(0, i).join("/");
        counts.set(prefix, (counts.get(prefix) ?? 0) + 1);
      }
      for (const t of r.tags) tags.add(t);
    }

    return HttpResponse.json({
      folders: [...counts.entries()]
        .map(([path, count]) => ({ path, count }))
        .sort((a, b) => a.path.localeCompare(b.path)),
      tags: [...tags].sort(),
    });
  }),

  http.get("/api/v1/resources", ({ request }) => {
    const url = new URL(request.url);
    const scope = url.searchParams.get("scope");
    const scopeId = url.searchParams.get("scope_id");
    const path = url.searchParams.get("path");
    const tag = url.searchParams.get("tag");
    const q = url.searchParams.get("q");

    let filtered = [...mockResources.resources];
    if (scope) filtered = filtered.filter((r) => r.scope === scope);
    if (scopeId) filtered = filtered.filter((r) => r.scope_id === scopeId);
    // A folder and everything beneath it, which is the prefix predicate the
    // store applies (pkg/resource/store.go). Matching only the exact path would
    // let a tree test pass here and show an empty folder against the server.
    if (path) {
      filtered = filtered.filter((r) => r.path === path || r.path.startsWith(path + "/"));
    }
    if (tag) {
      filtered = filtered.filter((r) =>
        r.tags.some((t: string) => t.toLowerCase().includes(tag.toLowerCase())),
      );
    }
    if (q) {
      const lower = q.toLowerCase();
      filtered = filtered.filter(
        (r) =>
          r.display_name.toLowerCase().includes(lower) ||
          r.description.toLowerCase().includes(lower),
      );
    }

    // The order the store returns, which is what the library renders in and now
    // also what a folder's files are ordered by (#1471). sort=last_read
    // puts read recency first with never-read last and falls back to update
    // recency, and anything else is update recency alone -- the two branches of
    // resource.Sort.orderByClause (pkg/resource/types.go).
    const byUpdated = (a: { updated_at: string }, b: { updated_at: string }) =>
      b.updated_at.localeCompare(a.updated_at);
    if (url.searchParams.get("sort") === "last_read") {
      filtered.sort((a, b) => {
        if (!a.last_read_at && !b.last_read_at) return byUpdated(a, b);
        if (!a.last_read_at) return 1;
        if (!b.last_read_at) return -1;
        return b.last_read_at.localeCompare(a.last_read_at) || byUpdated(a, b);
      });
    } else {
      filtered.sort(byUpdated);
    }

    const limit = Number(url.searchParams.get("limit")) || 100;
    const offset = Number(url.searchParams.get("offset")) || 0;
    return HttpResponse.json({
      resources: filtered.slice(offset, offset + limit),
      total: filtered.length,
    });
  }),

  // The capture routes a managed resource carries (#1554), which its library
  // tiles and its own Thumbnail panel read (#1568). Registered before the
  // by-id read below so "thumbnails" cannot be taken for a resource id.
  http.get("/api/v1/resources/thumbnails/pending", () => {
    // The server's predicate, which is what makes the queue terminate: a
    // capture is wanted when it is missing or older than the file, and the dark
    // one is asked for only of the families that carry one. Reading an empty
    // dark key as pending on a family that stores a single image would offer
    // the resource forever.
    const behind = (key?: string, at?: string, updated?: string) =>
      !key || !at || at < (updated ?? "");
    const pending = mockResources.resources.filter(
      (r) =>
        isThumbnailSupported(r.mime_type) &&
        r.size_bytes <= THUMBNAIL_SOURCE_LIMIT &&
        (behind(r.thumbnail_s3_key, r.thumbnail_captured_at, r.updated_at) ||
          (isThemeable(r.mime_type) &&
            behind(r.thumbnail_dark_s3_key, r.thumbnail_dark_captured_at, r.updated_at))),
    );
    return HttpResponse.json({ resources: pending, total: pending.length });
  }),

  // Where a capture the browser took is recorded. Without it the upload falls
  // through to the dev proxy and fails, the row never records a capture, and
  // the resource stays on the pending list -- so the queue re-fetches and
  // re-rasterizes it on every page for the life of the suite, on the same main
  // thread the tests are waiting on.
  //
  // The capture is dated to the resource's own updated_at, as the server dates
  // it: a resource row carries no version, so that timestamp is what says a
  // capture has caught up with the file it came from.
  http.put("/api/v1/resources/:id/thumbnail", async ({ params, request }) => {
    const resource = mockResources.resources.find((r) => r.id === params.id);
    if (!resource) {
      return HttpResponse.json({ error: "not found" }, { status: 404 });
    }
    const variant = new URL(request.url).searchParams.get("variant");
    const buffer = await request.arrayBuffer();
    if (variant === "dark") {
      thumbnailStore.set(`${resource.id}:dark`, buffer);
      resource.thumbnail_dark_s3_key = `thumbnails/${resource.id}_dark.png`;
      resource.thumbnail_dark_captured_at = resource.updated_at;
    } else {
      thumbnailStore.set(resource.id, buffer);
      resource.thumbnail_s3_key = `thumbnails/${resource.id}.png`;
      resource.thumbnail_captured_at = resource.updated_at;
    }
    return HttpResponse.json(resource);
  }),

  http.get("/api/v1/resources/:id/thumbnail", ({ params, request }) => {
    const id = params.id as string;
    const resource = mockResources.resources.find((r) => r.id === id);
    if (!resource?.thumbnail_s3_key) {
      return HttpResponse.json({ error: "no thumbnail" }, { status: 404 });
    }
    const variant = new URL(request.url).searchParams.get("variant");
    return serveThumbnail(id, variant) ?? HttpResponse.json({ error: "no thumbnail" }, { status: 404 });
  }),

  // Both variants, which is what the route does: two views of one file, and a
  // reader asking for the tile to be taken again means the tile.
  http.delete("/api/v1/resources/:id/thumbnail", ({ params }) => {
    const resource = mockResources.resources.find((r) => r.id === params.id);
    if (!resource) {
      return HttpResponse.json({ error: "not found" }, { status: 404 });
    }
    resource.thumbnail_s3_key = undefined;
    resource.thumbnail_dark_s3_key = undefined;
    resource.thumbnail_captured_at = undefined;
    resource.thumbnail_dark_captured_at = undefined;
    return new HttpResponse(null, { status: 204 });
  }),

  // The detail read is the only one that carries usage: the server consults the
  // audit rollup here and nowhere else.
  http.get("/api/v1/resources/:id", ({ params }) => {
    const resource = mockResources.resources.find((r) => r.id === params.id);
    if (!resource) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const usage = mockResourceUsage[resource.id as string];
    return HttpResponse.json(usage ? { ...resource, usage } : resource);
  }),

  // The metadata edit, and the move that travels with it (#1502). The move is
  // modelled rather than acknowledged: it rewrites the library AND the canonical
  // URI on the fixture, which is what the dialog's own note promises and what a
  // reader checks on the page behind it.
  http.patch("/api/v1/resources/:id", async ({ params, request }) => {
    const resource = mockResources.resources.find((r) => r.id === params.id);
    if (!resource) {
      return HttpResponse.json({ error: "not found" }, { status: 404 });
    }
    const body = (await request.json()) as Record<string, unknown>;
    if (typeof body.display_name === "string") resource.display_name = body.display_name;
    if (typeof body.description === "string") resource.description = body.description;
    // A folder change rewrites the canonical URI, which is #1528: the stored
    // path and the address printed beside it are the same fact, and the fixture
    // has to move both or a test passes on a disagreement the server no longer
    // allows.
    if (typeof body.path === "string") {
      resource.uri = resource.uri.replace(
        `/${resource.path}/${resource.filename}`,
        `/${body.path}/${resource.filename}`,
      );
      resource.path = body.path;
    }
    if (Array.isArray(body.tags)) resource.tags = body.tags as string[];
    if (typeof body.scope === "string") {
      const scopeID = typeof body.scope_id === "string" ? body.scope_id : "";
      const tail = `${resource.path}/${resource.filename}`;
      resource.scope = body.scope as typeof resource.scope;
      resource.scope_id = scopeID;
      resource.uri =
        body.scope === "global"
          ? `mcp://global/${tail}`
          : `mcp://${body.scope}/${scopeID}/${tail}`;
    }
    resource.updated_at = new Date().toISOString();
    return HttpResponse.json(resource);
  }),

  // The folder move (#1529): one prefix rewrite over every resource beneath the
  // folder, in one transaction. It is modelled rather than acknowledged --
  // every matching fixture's path and URI move together -- because the tree
  // redraws off the listing afterwards and an acknowledgement would leave the
  // folder still standing there.
  http.post("/api/v1/resources/folders/move", async ({ request }) => {
    const body = (await request.json()) as { scope: string; scope_id?: string; from: string; to: string };
    const inLibrary = mockResources.resources.filter(
      (r) => r.scope === body.scope && (r.scope_id ?? "") === (body.scope_id ?? ""),
    );
    const beneath = inLibrary.filter(
      (r) => r.path === body.from || r.path.startsWith(body.from + "/"),
    );
    if (beneath.length === 0) {
      return HttpResponse.json(
        { error: "no resources are filed under that folder" },
        { status: 404 },
      );
    }
    const moved = beneath.map((r) => {
      const from_uri = r.uri;
      const next = body.to + r.path.slice(body.from.length);
      r.uri = r.uri.replace(`/${r.path}/${r.filename}`, `/${next}/${r.filename}`);
      r.path = next;
      return { id: r.id, path: r.path, uri: r.uri, from_uri };
    });
    return HttpResponse.json({ from: body.from, to: body.to, moved });
  }),

  // Content download: the preview pane, the Download button and the library's
  // image tiles all read it. Text fixtures render in the preview; an image
  // fixture answers with real bytes, there being no stored thumbnail for a
  // resource and a tile therefore being the object itself; anything else is a
  // byte blob the viewer reports by type.
  http.get("/api/v1/resources/:id/content", ({ params }) => {
    const resource = mockResources.resources.find((r) => r.id === params.id);
    if (!resource) {
      return HttpResponse.json({ error: "not found" }, { status: 404 });
    }
    const image = resourceImageBytes(String(params.id));
    if (image) {
      return new HttpResponse(image, { headers: { "Content-Type": resource.mime_type } });
    }
    return new HttpResponse(resourceBody(resource), {
      headers: { "Content-Type": resource.mime_type },
    });
  }),

  http.get("/api/v1/resources/:id/versions", ({ params }) => {
    const resource = mockResources.resources.find((r) => r.id === params.id);
    if (!resource) {
      return HttpResponse.json({ error: "not found" }, { status: 404 });
    }
    const versions = mockResourceVersions[resource.id as string] ?? [];
    const current = versions[0]?.version ?? 0;
    return HttpResponse.json({ versions, current, max_versions: 10 });
  }),

  // Replacing content returns the resource with its identity intact — the same
  // id, uri, and filename — which is what the panel asserts.
  http.post("/api/v1/resources/:id/content", ({ params }) => {
    const resource = mockResources.resources.find((r) => r.id === params.id);
    if (!resource) {
      return HttpResponse.json({ error: "not found" }, { status: 404 });
    }
    return HttpResponse.json({ ...resource, updated_at: new Date().toISOString() });
  }),

  http.post("/api/v1/resources/:id/versions/:version/restore", ({ params }) => {
    const resource = mockResources.resources.find((r) => r.id === params.id);
    if (!resource) {
      return HttpResponse.json({ error: "not found" }, { status: 404 });
    }
    return HttpResponse.json({ ...resource, updated_at: new Date().toISOString() });
  }),

  http.get("/api/v1/resources/:id/versions/:version/content", ({ params }) => {
    const versions = mockResourceVersions[params.id as string] ?? [];
    const version = versions.find((v) => String(v.version) === params.version);
    if (!version) {
      return HttpResponse.json({ error: "not found" }, { status: 404 });
    }
    return new HttpResponse(`contents of version ${version.version}`, {
      headers: { "Content-Type": "text/plain" },
    });
  }),

  // =========================================================================
  // Table registrations (#1327) — shared by resources and portal assets
  //
  // Register and drop mutate the fixture map so a demo, a screenshot run, and
  // an e2e walk all see the panel change rather than a control that appears to
  // do nothing.
  // =========================================================================

  http.get("/api/v1/table-connections", () =>
    HttpResponse.json({ connections: mockTableConnections }),
  ),

  // The cross-source listing (#1472): every registration this reader may see,
  // whichever kind of file it was built over.
  http.get("/api/v1/tables", ({ request }) =>
    HttpResponse.json(mockScratchTableList(new URL(request.url))),
  ),

  http.get("/api/v1/tables/:regId", ({ params }) => {
    const row = mockScratchTable(params.regId as string);
    if (!row) {
      return HttpResponse.json(
        { type: "about:blank", title: "Not Found", status: 404, detail: "no such registered table" },
        { status: 404 },
      );
    }
    return HttpResponse.json(row);
  }),

  http.get("/api/v1/resources/:id/tables", ({ params }) =>
    HttpResponse.json({ registrations: mockTableRegistrations[params.id as string] ?? [] }),
  ),

  http.post("/api/v1/resources/:id/tables", async ({ params, request }) =>
    tableRegisterResponse(await mockRegisterTable("resource", params.id as string, request)),
  ),

  http.delete("/api/v1/resources/:id/tables/:regId", ({ params }) => {
    mockDropTable(params.id as string, params.regId as string);
    return new HttpResponse(null, { status: 204 });
  }),

  http.get("/api/v1/portal/assets/:id/tables", ({ params }) =>
    HttpResponse.json({ registrations: mockTableRegistrations[params.id as string] ?? [] }),
  ),

  http.post("/api/v1/portal/assets/:id/tables", async ({ params, request }) =>
    tableRegisterResponse(await mockRegisterTable("asset", params.id as string, request)),
  ),

  http.delete("/api/v1/portal/assets/:id/tables/:regId", ({ params }) => {
    mockDropTable(params.id as string, params.regId as string);
    return new HttpResponse(null, { status: 204 });
  }),

  // =========================================================================
  // Portal — Prompts
  // =========================================================================

  http.get(`${PORTAL_BASE}/prompts`, () => {
    return HttpResponse.json(mockPortalPrompts);
  }),

  // Ranked prompt search: substring match over the caller's visible prompts,
  // returned as scored results in descending order (mock relevance).
  http.get(`${PORTAL_BASE}/prompts/search`, ({ request }) => {
    const url = new URL(request.url);
    const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    const visible = [...mockPortalPrompts.personal, ...mockPortalPrompts.available];
    const matches = q
      ? visible.filter(
          (p) =>
            (p.name ?? "").toLowerCase().includes(q) ||
            (p.display_name ?? "").toLowerCase().includes(q) ||
            (p.description ?? "").toLowerCase().includes(q) ||
            (p.content ?? "").toLowerCase().includes(q),
        )
      : [];
    const data = matches.map((p, i) => ({ prompt: p, score: 1 - i * 0.05 }));
    return HttpResponse.json({ data, total: data.length, limit: 20, offset: 0 });
  }),

  // Prompts explicitly shared with the caller, and per-prompt share lists.
  // The portal prompt viewer queries both on load; no shares are configured in
  // the mock, so both return empty (a clean "not shared" state).
  http.get(`${PORTAL_BASE}/shared-prompts`, () => HttpResponse.json(mockSharedPrompts)),

  http.get(`${PORTAL_BASE}/prompts/:id/shares`, () => HttpResponse.json([])),

  // Usage rollup (#1009): run count + last run per visible prompt id.
  http.get(`${PORTAL_BASE}/prompts/usage`, () => HttpResponse.json(mockPromptUsage)),

  // Prompt resource attachments (#1013). Stateful in the mock so attach,
  // reorder, and detach are exercisable end-to-end in MSW mode. A prompt starts
  // with a readable template, a deleted resource (the broken-link state), and a
  // persona resource the current user cannot read (the restricted state), so
  // every rendered state is reachable without server-side setup.
  http.get(`${PORTAL_BASE}/prompts/:id/attachments`, ({ params }) =>
    HttpResponse.json(promptAttachmentList(String(params.id))),
  ),

  http.post(`${PORTAL_BASE}/prompts/:id/attachments`, async ({ params, request }) => {
    const body = (await request.json()) as { resource_id?: string };
    const promptId = String(params.id);
    const resourceId = body.resource_id ?? "";
    const res = mockResources.resources.find((r) => r.id === resourceId);
    if (!res) {
      return HttpResponse.json({ error: "resource not found" }, { status: 404 });
    }
    // Mirror the server's scope rule so the portal's error path is reachable:
    // a user-scoped resource cannot go on a shared prompt.
    const prompt = allMockPrompts().find((p) => p.id === promptId);
    if (res.scope === "user" && prompt && prompt.scope !== "personal") {
      return HttpResponse.json(
        { error: `resource "${res.display_name}" cannot be attached: a private resource can only be attached to a personal prompt` },
        { status: 409 },
      );
    }
    const current = statefulPromptAttachments[promptId] ?? [];
    if (!current.includes(resourceId)) {
      statefulPromptAttachments[promptId] = [...current, resourceId];
    }
    return HttpResponse.json(promptAttachmentList(promptId));
  }),

  http.put(`${PORTAL_BASE}/prompts/:id/attachments`, async ({ params, request }) => {
    const body = (await request.json()) as { resource_ids?: string[] };
    const promptId = String(params.id);
    statefulPromptAttachments[promptId] = body.resource_ids ?? [];
    return HttpResponse.json(promptAttachmentList(promptId));
  }),

  http.delete(`${PORTAL_BASE}/prompts/:id/attachments/:resourceId`, ({ params }) => {
    const promptId = String(params.id);
    const current = statefulPromptAttachments[promptId] ?? [];
    if (!current.includes(String(params.resourceId))) {
      return HttpResponse.json({ error: "attachment not found" }, { status: 404 });
    }
    statefulPromptAttachments[promptId] = current.filter((id) => id !== String(params.resourceId));
    return HttpResponse.json({ status: "detached" });
  }),

  http.get(`${PORTAL_BASE}/resources/:id/prompts`, ({ params }) => {
    const resourceId = String(params.id);
    const data = Object.entries(statefulPromptAttachments)
      .filter(([, ids]) => ids.includes(resourceId))
      .map(([promptId]) => allMockPrompts().find((p) => p.id === promptId))
      .filter((p): p is NonNullable<typeof p> => Boolean(p))
      .map((p) => ({ id: p.id, name: p.name, display_name: p.display_name, scope: p.scope }));
    return HttpResponse.json({ data, total: data.length });
  }),

  // Version history (#1009/#1010): newest first, with approval provenance.
  http.get(`${PORTAL_BASE}/prompts/:id/versions`, ({ params }) => {
    const versions = mockPromptVersions[String(params.id)] ?? [];
    return HttpResponse.json({ data: versions, total: versions.length });
  }),

  // Collections (#1010): stateful in the mock so the manage/create/assign
  // flows are exercisable end-to-end in MSW mode.
  http.get(`${PORTAL_BASE}/prompt-collections`, () => {
    // Counts are computed per read like the real server's LEFT JOIN COUNT, so
    // they stay honest after assignments.
    const allPrompts = [
      ...mockPortalPrompts.personal,
      ...mockPortalPrompts.available,
      ...mockSharedPrompts.map((s) => s.prompt),
    ];
    const data = statefulPromptCollections.map((c) => ({
      ...c,
      prompt_count: allPrompts.filter((p) => p.collection_id === c.id).length,
    }));
    return HttpResponse.json({ data, total: data.length });
  }),

  http.post(`${PORTAL_BASE}/prompt-collections`, async ({ request }) => {
    const body = (await request.json()) as { name?: string; description?: string };
    const name = (body.name ?? "").trim();
    if (!name) {
      return HttpResponse.json({ error: "collection name is required" }, { status: 400 });
    }
    if (statefulPromptCollections.some((c) => c.name.toLowerCase() === name.toLowerCase())) {
      return HttpResponse.json({ error: "a collection with that name already exists" }, { status: 409 });
    }
    const created = {
      id: `pcol-${String(statefulPromptCollections.length + 1).padStart(3, "0")}`,
      name,
      description: body.description ?? "",
      created_by: "admin@example.com",
      prompt_count: 0,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    statefulPromptCollections.push(created);
    return HttpResponse.json(created, { status: 201 });
  }),

  http.put(`${PORTAL_BASE}/prompt-collections/:id`, async ({ params, request }) => {
    const col = statefulPromptCollections.find((c) => c.id === params.id);
    if (!col) {
      return HttpResponse.json({ error: "collection not found" }, { status: 404 });
    }
    const body = (await request.json()) as { name?: string; description?: string };
    const name = (body.name ?? "").trim();
    // Mirror the real server: empty names are 400, renaming onto another
    // collection's name (case-insensitively) is 409.
    if (!name) {
      return HttpResponse.json({ error: "collection name is required" }, { status: 400 });
    }
    if (statefulPromptCollections.some((c) => c.id !== params.id && c.name.toLowerCase() === name.toLowerCase())) {
      return HttpResponse.json({ error: "a collection with that name already exists" }, { status: 409 });
    }
    col.name = name;
    col.description = body.description ?? col.description;
    col.updated_at = new Date().toISOString();
    return HttpResponse.json(col);
  }),

  http.delete(`${PORTAL_BASE}/prompt-collections/:id`, ({ params }) => {
    const idx = statefulPromptCollections.findIndex((c) => c.id === params.id);
    if (idx === -1) {
      return HttpResponse.json({ error: "collection not found" }, { status: 404 });
    }
    statefulPromptCollections.splice(idx, 1);
    return HttpResponse.json({ status: "deleted" });
  }),

  // Assignment (#1010): stamp the prompt fixture so subsequent list reads
  // reflect the move.
  http.put(`${PORTAL_BASE}/prompts/:id/collection`, async ({ params, request }) => {
    const body = (await request.json()) as { collection_id?: string };
    const all = [
      ...mockPortalPrompts.personal,
      ...mockPortalPrompts.available,
      ...mockSharedPrompts.map((s) => s.prompt),
    ];
    const target = all.find((p) => p.id === params.id);
    if (!target) {
      return HttpResponse.json({ error: "prompt not found" }, { status: 404 });
    }
    if (body.collection_id && !statefulPromptCollections.some((c) => c.id === body.collection_id)) {
      return HttpResponse.json({ error: "collection not found" }, { status: 404 });
    }
    target.collection_id = body.collection_id || undefined;
    return HttpResponse.json(target);
  }),

  // =========================================================================
  // Admin — Prompts
  // =========================================================================

  http.get(`${ADMIN_BASE}/prompts`, ({ request }) => {
    const url = new URL(request.url);
    const scope = url.searchParams.get("scope");
    const ownerEmail = url.searchParams.get("owner_email");

    let filtered = [...mockAdminPrompts];
    if (scope) filtered = filtered.filter((p) => p.scope === scope);
    if (ownerEmail)
      filtered = filtered.filter((p) => p.owner_email === ownerEmail);
    // The review queue requests ?review_requested=true; honor it so the
    // pending-promotions banner shows only prompts actually awaiting review,
    // not every catalogued prompt.
    if (url.searchParams.get("review_requested") === "true")
      filtered = filtered.filter((p) => p.review_requested === true);

    return HttpResponse.json({
      data: filtered,
      total: filtered.length,
    });
  }),

  http.get(`${ADMIN_BASE}/prompts/:id`, ({ params }) => {
    const prompt = mockAdminPrompts.find((p) => p.id === params.id);
    if (!prompt) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    return HttpResponse.json(prompt);
  }),

  // =========================================================================
  // Admin — Config
  // =========================================================================

  http.get(`${ADMIN_BASE}/config/entries`, () => {
    return HttpResponse.json(mockConfigEntries);
  }),

  http.get(`${ADMIN_BASE}/config/entries/:key`, ({ params }) => {
    const entry = mockConfigEntries.find((e) => e.key === params.key);
    if (!entry) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    return HttpResponse.json(entry);
  }),

  http.put(
    `${ADMIN_BASE}/config/entries/:key`,
    async ({ params, request }) => {
      const entry = mockConfigEntries.find((e) => e.key === params.key);
      if (!entry) {
        return HttpResponse.json({ detail: "Not found" }, { status: 404 });
      }
      const body = (await request.json()) as Record<string, unknown>;
      return HttpResponse.json({
        ...entry,
        value: body.value as string,
        updated_at: new Date().toISOString(),
      });
    },
  ),

  http.delete(`${ADMIN_BASE}/config/entries/:key`, () => {
    return new HttpResponse(null, { status: 204 });
  }),

  http.get(`${ADMIN_BASE}/config/effective`, () => {
    return HttpResponse.json(mockEffectiveConfig);
  }),

  http.get(`${ADMIN_BASE}/config/changelog`, ({ request }) => {
    const url = new URL(request.url);
    const limit = Number(url.searchParams.get("limit")) || 50;
    const offset = Number(url.searchParams.get("offset")) || 0;
    return HttpResponse.json({
      entries: mockConfigChangelog.slice(offset, offset + limit),
      total: mockConfigChangelog.length,
    });
  }),

  // Platform-owned agent-instructions baseline (#646). Rendered read-only
  // beneath the admin's editable instructions on the Agent Instructions
  // page; previously unhandled, so the baseline panel rendered empty.
  http.get(`${ADMIN_BASE}/config/agent-instructions-baseline`, () => {
    return HttpResponse.json({
      baseline: [
        "# How to operate this platform",
        "",
        "You have access to a semantic data platform. Discover before you act:",
        "call `search` first — one query spans the catalog, knowledge pages,",
        "captured insights, saved assets, prompts, and API connections.",
        "",
        "## Querying data",
        "- Use `trino_query` for the warehouse; `trino_describe_table` first to",
        "  confirm columns and semantics.",
        "- Reach the DataHub catalog with `search`, then `fetch` a result's reference.",
        "- List and read S3 objects with `s3_list` / `s3_object`.",
        "",
        "## Delivering work",
        "- Persist every report, dashboard, or document with `save_asset` so",
        "  it lands in the portal with a shareable link.",
        "- Capture durable findings with `memory_capture`; they enter review and",
        "  promote to shared knowledge so no one re-teaches the same fact.",
        "",
        "This baseline is composed beneath the administrator's own instructions",
        "and names only the tools this deployment exposes.",
      ].join("\n"),
    });
  }),

  // =========================================================================
  // Admin: Settings (SMTP, #631)
  // =========================================================================

  http.get(`${ADMIN_BASE}/settings/smtp`, () => {
    smtpSettings.warnings = smtpWarnings();
    return HttpResponse.json(smtpSettings);
  }),

  http.put(`${ADMIN_BASE}/settings/smtp`, async ({ request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    smtpSettings.enabled = Boolean(body.enabled);
    smtpSettings.host = String(body.host ?? "");
    smtpSettings.port = Number(body.port ?? 587);
    smtpSettings.username = String(body.username ?? "");
    // Empty password keeps the stored one; a non-empty one replaces it.
    if (typeof body.password === "string" && body.password !== "") {
      smtpSettings.password_set = true;
    }
    smtpSettings.from = String(body.from ?? "");
    smtpSettings.from_name = String(body.from_name ?? "");
    smtpSettings.tls_mode = String(body.tls_mode ?? "starttls");
    smtpSettings.updated_by = "sarah.chen@example.com";
    smtpSettings.updated_at = new Date().toISOString();
    smtpSettings.warnings = smtpWarnings();
    return HttpResponse.json(smtpSettings);
  }),

  // Recipient opt-out state for the test-send notice (#1022). The fixture
  // address optedout@example.com reads as opted out; everything else does not.
  http.get(`${ADMIN_BASE}/settings/smtp/recipient-status`, ({ request }) => {
    const to = (new URL(request.url).searchParams.get("to") ?? "").toLowerCase();
    if (!to.includes("@")) {
      return HttpResponse.json({ detail: "to must be a valid email address" }, { status: 400 });
    }
    return HttpResponse.json({ to, opted_out: to === "optedout@example.com" });
  }),

  http.post(`${ADMIN_BASE}/settings/smtp/test`, async ({ request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    if (!smtpSettings.enabled) {
      return HttpResponse.json(
        { detail: "SMTP is disabled; enable and save the settings first" },
        { status: 409 },
      );
    }
    return HttpResponse.json({ status: "sent", to: String(body.to ?? "") });
  }),

  // =========================================================================
  // Admin: Settings (knowledge review-queue alert, #803)
  // =========================================================================

  http.get(`${ADMIN_BASE}/settings/review-queue-alert`, () => {
    reviewQueueAlert.warnings = reviewQueueAlertWarnings();
    return HttpResponse.json(reviewQueueAlert);
  }),

  http.put(`${ADMIN_BASE}/settings/review-queue-alert`, async ({ request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    reviewQueueAlert.enabled = Boolean(body.enabled);
    reviewQueueAlert.pending_threshold = Number(body.pending_threshold ?? 0);
    reviewQueueAlert.oldest_pending_days = Number(body.oldest_pending_days ?? 0);
    reviewQueueAlert.cooldown_hours = Number(body.cooldown_hours ?? 24);
    // The server normalizes recipients to the bare, lowercased address.
    reviewQueueAlert.recipients = (Array.isArray(body.recipients) ? body.recipients : [])
      .map((r) => String(r).trim().toLowerCase());
    reviewQueueAlert.updated_by = "sarah.chen@example.com";
    reviewQueueAlert.updated_at = new Date().toISOString();
    reviewQueueAlert.warnings = reviewQueueAlertWarnings();
    return HttpResponse.json(reviewQueueAlert);
  }),

  // =========================================================================
  // Admin — Keys
  // =========================================================================

  http.get(`${ADMIN_BASE}/auth/keys`, () => {
    return HttpResponse.json(mockAPIKeys);
  }),

  http.post(`${ADMIN_BASE}/auth/keys`, async ({ request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    return HttpResponse.json({
      name: (body.name as string) ?? "new-key",
      key: `mck_${Math.random().toString(36).slice(2, 34)}`,
      roles: (body.roles as string[]) ?? ["viewer"],
      warning:
        "Store this key securely. It will not be shown again.",
    });
  }),

  http.delete(`${ADMIN_BASE}/auth/keys/:name`, () => {
    return new HttpResponse(null, { status: 204 });
  }),

  // =========================================================================
  // Portal — Memory
  // =========================================================================

  http.get(`${PORTAL_BASE}/memory/records`, ({ request }) => {
    const url = new URL(request.url);
    const dimension = url.searchParams.get("dimension");
    const category = url.searchParams.get("category");
    const status = url.searchParams.get("status");
    const source = url.searchParams.get("source");
    const limit = parseInt(url.searchParams.get("limit") ?? "20", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    let filtered = [...mockPortalMemoryRecords];
    if (dimension) filtered = filtered.filter((r) => r.dimension === dimension);
    if (category) filtered = filtered.filter((r) => r.category === category);
    if (status) filtered = filtered.filter((r) => r.status === status);
    if (source) filtered = filtered.filter((r) => r.source === source);

    const data = filtered.slice(offset, offset + limit);
    return HttpResponse.json({
      data,
      total: filtered.length,
      limit,
      offset,
    });
  }),

  http.get(`${PORTAL_BASE}/memory/records/stats`, () => {
    return HttpResponse.json(mockPortalMemoryStats);
  }),

  // =========================================================================
  // Portal: Notification preferences (#631)
  // =========================================================================

  http.get(`${PORTAL_BASE}/notification-prefs`, () => {
    return HttpResponse.json(notificationPrefs);
  }),

  http.put(`${PORTAL_BASE}/notification-prefs`, async ({ request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    // Partial update: omitted fields are left unchanged.
    if (typeof body.mode === "string") notificationPrefs.mode = body.mode;
    if (typeof body.shares_enabled === "boolean") {
      notificationPrefs.shares_enabled = body.shares_enabled;
    }
    if (typeof body.mentions_enabled === "boolean") {
      notificationPrefs.mentions_enabled = body.mentions_enabled;
    }
    if (typeof body.comments_enabled === "boolean") {
      notificationPrefs.comments_enabled = body.comments_enabled;
    }
    return HttpResponse.json(notificationPrefs);
  }),

  // =========================================================================
  // Portal + Admin: Notification delivery history (#1016)
  // =========================================================================

  // The portal endpoint is self-scoped server-side: it takes no recipient, so
  // the mock returns the same rows regardless of who asks.
  http.get(`${PORTAL_BASE}/notifications`, () => {
    return HttpResponse.json({
      data: mockNotificationRows.map(
        ({ recipient: _recipient, attempts: _attempts, last_error: _lastError, scheduled_for: _scheduled, ...item }) =>
          item,
      ),
      total: mockNotificationRows.length,
      page: 1,
      per_page: 20,
      retention_days: 30,
    });
  }),

  http.get(`${ADMIN_BASE}/notifications`, ({ request }) => {
    const params = new URL(request.url).searchParams;
    const status = params.get("status");
    const rows = status
      ? mockNotificationRows.filter((n) => n.status === status)
      : mockNotificationRows;
    return HttpResponse.json({ data: rows, total: rows.length, page: 1, per_page: 20 });
  }),

  http.get(`${ADMIN_BASE}/notifications/stats`, () => {
    return HttpResponse.json({
      pending: 2,
      sending: 0,
      sent: 128,
      failed: 3,
      total: 133,
      retention_days: 30,
    });
  }),

  // =========================================================================
  // Portal - Unified knowledge search (#661)
  // =========================================================================

  http.get(`${PORTAL_BASE}/search`, ({ request }) => {
    const url = new URL(request.url);
    const q = (url.searchParams.get("q") ?? "").trim();
    const sources = url.searchParams.getAll("sources");
    if (!q && url.searchParams.getAll("entity_urns").length === 0) {
      return HttpResponse.json(
        { error: "q or entity_urns is required" },
        { status: 400 },
      );
    }
    const groups = [
      {
        source: "datahub",
        hits: [
          {
            text: `daily_sales (matches "${q}")`,
            source: "datahub",
            ref: "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.daily_sales,PROD)",
            score: 0.94,
            entity_urns: [
              "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.daily_sales,PROD)",
            ],
            dimension: "dataset",
          },
          {
            text: "retail_locations",
            source: "datahub",
            ref: "urn:li:dataset:(urn:li:dataPlatform:trino,hive.ref.retail_locations,PROD)",
            score: 0.71,
            entity_urns: [
              "urn:li:dataset:(urn:li:dataPlatform:trino,hive.ref.retail_locations,PROD)",
            ],
          },
        ],
      },
      {
        // Refs are real mock page ids (kp-seed-*) so "Open page" resolves.
        source: "knowledge_pages",
        hits: [
          {
            text: "Revenue Definition",
            source: "knowledge_pages",
            ref: "kp-seed-2",
            score: 0.88,
          },
          {
            text: "Fiscal Calendar",
            source: "knowledge_pages",
            ref: "kp-seed-1",
            score: 0.61,
          },
        ],
      },
      {
        source: "insights",
        hits: [
          {
            text: "Loyalty points are not recognized as revenue.",
            source: "insights",
            ref: "ins_loyalty_points",
            score: 0.78,
            status: "pending",
            entity_urns: [
              "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.daily_sales,PROD)",
            ],
          },
        ],
      },
      {
        source: "memory",
        hits: [
          {
            text: "The revenue column excludes returns.",
            source: "memory",
            ref: "mem_revenue_returns",
            score: 0.66,
            dimension: "business_knowledge",
          },
        ],
      },
      {
        // Real mock asset id so "Open asset" resolves.
        source: "assets",
        hits: [
          {
            text: "Q3 Revenue Dashboard",
            source: "assets",
            ref: "ast-001",
            score: 0.64,
          },
        ],
      },
      {
        // A real mock session id so "Open session" resolves. The caller's own
        // work is a search source (#1322): the session is found by what its
        // calls said they were for.
        source: "sessions",
        hits: [
          {
            text:
              "Sizing Q3 revenue by region for the board deck.\n" +
              "Saved: Q3 Revenue Dashboard\n6 calls on 2026-08-16",
            source: "sessions",
            ref: agentSessions[0]!,
            score: 0.62,
          },
        ],
      },
      {
        // Real mock prompt id so "Open prompt" resolves.
        source: "prompts",
        hits: [
          {
            text: "Churn cohort analysis",
            source: "prompts",
            ref: "prompt-001",
            score: 0.6,
          },
        ],
      },
    ];
    const filtered = sources.length
      ? groups.filter((g) => sources.includes(g.source))
      : groups;
    // The datahub group stands in for a source the search ranked as deep as its
    // budget went and no further: its matched is a floor, and the coverage line
    // renders it "25+" rather than "25" (#1585).
    const coverage = filtered.map((g) => ({
      source: g.source,
      matched: g.source === "datahub" ? 25 : g.hits.length + 4,
      shown: g.hits.length,
      ...(g.source === "datahub" ? { matched_capped: true } : {}),
    }));
    const count = filtered.reduce((n, g) => n + g.hits.length, 0);
    return HttpResponse.json({ groups: filtered, coverage, count, ranking: "hybrid" });
  }),

  // Feature-area handler modules (issue #878). Spread last so any existing
  // handler above wins on an overlapping path; these only cover endpoints
  // that were previously unhandled (and rendered empty in demos/docs).
  ...catalogHandlers,
  ...apiBrowseHandlers,
  ...userHandlers,
  ...connectionInstanceHandlers,
  ...scriptHandlers,
  ...sessionHandlers,
  ...callHandlers,
  ...assetRefHandlers,
  ...producerHandlers,
];
