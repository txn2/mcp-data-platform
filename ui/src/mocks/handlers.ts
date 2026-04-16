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
import { mockCollections, mockSharedCollections } from "./data/collections";
import { mockAdminPrompts, mockPortalPrompts } from "./data/prompts";
import { mockResources } from "./data/resources";
import { mockAPIKeys } from "./data/keys";
import {
  mockEffectiveConfig,
  mockConfigEntries,
  mockConfigChangelog,
} from "./data/config";

import {
  mockMemoryRecords,
  mockMemoryStats,
  mockPortalMemoryRecords,
  mockPortalMemoryStats,
} from "./data/memory";

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

const portalAssets = [
  ...mockAssets,
  ...mockSharedWithMe.map((s) => s.asset),
];

const thumbnailStore = new Map<string, ArrayBuffer>();

const THUMB_COLORS: Record<string, string> = {
  "text/html": "#3b82f6",
  "image/svg+xml": "#8b5cf6",
  "text/markdown": "#10b981",
  "text/jsx": "#f59e0b",
  "text/csv": "#06b6d4",
};

const STATIC_THUMBNAILS: Record<string, string> = {
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
};
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
      version: mockSystemInfo.version,
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

  http.get(`${ADMIN_BASE}/connection-instances/effective`, () => {
    return HttpResponse.json([
      {
        kind: "trino",
        name: "acme-warehouse",
        connection: "acme-warehouse",
        description:
          "Production data warehouse with retail, inventory, and analytics schemas",
        source: "file",
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
        tools: [
          "datahub_search",
          "datahub_get_entity",
          "datahub_get_schema",
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
        tools: ["datahub_search", "datahub_get_entity"],
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
        tools: ["s3_list_objects", "s3_get_object", "s3_list_buckets"],
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
        tools: ["s3_list_objects", "s3_get_object"],
        config: { region: "us-west-2", bucket: "acme-reports-prod" },
        updated_at: new Date(Date.now() - 10 * 86400000).toISOString(),
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

  http.put(`${PORTAL_BASE}/assets/:id/thumbnail`, async ({ params, request }) => {
    const asset = portalAssets.find((a) => a.id === params.id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const buffer = await request.arrayBuffer();
    thumbnailStore.set(asset.id, buffer);
    asset.thumbnail_s3_key = `thumbnails/${asset.id}.png`;
    return new HttpResponse(null, { status: 204 });
  }),

  http.get(`${PORTAL_BASE}/assets/:id/thumbnail`, ({ params }) => {
    const id = params.id as string;
    const buffer = thumbnailStore.get(id);
    if (buffer) {
      return new HttpResponse(buffer, {
        headers: { "Content-Type": "image/png" },
      });
    }
    if (STATIC_THUMBNAILS[id]) {
      return new HttpResponse(STATIC_THUMBNAILS[id], { headers: { "Content-Type": "image/svg+xml" } });
    }
    const asset = portalAssets.find((a) => a.id === id);
    if (asset?.content_type.includes("svg") && mockContent[id]) {
      return new HttpResponse(mockContent[id], { headers: { "Content-Type": "image/svg+xml" } });
    }
    return new HttpResponse(null, { status: 404 });
  }),

  http.get(`${ADMIN_BASE}/assets/:id/thumbnail`, ({ params }) => {
    const id = params.id as string;
    const buffer = thumbnailStore.get(id);
    if (buffer) {
      return new HttpResponse(buffer, {
        headers: { "Content-Type": "image/png" },
      });
    }
    if (STATIC_THUMBNAILS[id]) {
      return new HttpResponse(STATIC_THUMBNAILS[id], { headers: { "Content-Type": "image/svg+xml" } });
    }
    return new HttpResponse(null, { status: 404 });
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
    if (!buffer) return new HttpResponse(null, { status: 404 });
    return new HttpResponse(buffer, {
      headers: { "Content-Type": "image/png" },
    });
  }),

  http.put(`${PORTAL_BASE}/collections/:id/thumbnail`, async ({ params, request }) => {
    const id = params.id as string;
    const buffer = await request.arrayBuffer();
    thumbnailStore.set(`col-${id}`, buffer);
    const col = mockCollections.find((c) => c.id === id);
    if (col) (col as Record<string, unknown>).thumbnail_s3_key = `thumbnails/col-${id}.png`;
    return new HttpResponse(null, { status: 204 });
  }),

  http.get(`${PORTAL_BASE}/collections/:id`, ({ params }) => {
    const col = mockCollections.find((c) => c.id === params.id);
    if (!col) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const enriched = {
      ...col,
      is_owner: true,
      sections: (col.sections ?? []).map((s) => ({
        ...s,
        items: (s.items ?? []).map((item) => {
          const asset = portalAssets.find((a) => a.id === item.asset_id);
          return {
            ...item,
            asset_thumbnail_s3_key: asset?.thumbnail_s3_key,
          };
        }),
      })),
    };
    return HttpResponse.json(enriched);
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
    () => {
      return HttpResponse.json([]);
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

  http.get("/api/v1/resources", ({ request }) => {
    const url = new URL(request.url);
    const scope = url.searchParams.get("scope");
    const scopeId = url.searchParams.get("scope_id");
    const category = url.searchParams.get("category");
    const tag = url.searchParams.get("tag");
    const q = url.searchParams.get("q");

    let filtered = [...mockResources.resources];
    if (scope) filtered = filtered.filter((r) => r.scope === scope);
    if (scopeId) filtered = filtered.filter((r) => r.scope_id === scopeId);
    if (category) filtered = filtered.filter((r) => r.category === category);
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

    return HttpResponse.json({
      resources: filtered,
      total: filtered.length,
    });
  }),

  http.get("/api/v1/resources/:id", ({ params }) => {
    const resource = mockResources.resources.find((r) => r.id === params.id);
    if (!resource) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    return HttpResponse.json(resource);
  }),

  // =========================================================================
  // Portal — Prompts
  // =========================================================================

  http.get(`${PORTAL_BASE}/prompts`, () => {
    return HttpResponse.json(mockPortalPrompts);
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

  http.get(`${ADMIN_BASE}/config/changelog`, () => {
    return HttpResponse.json(mockConfigChangelog);
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
  // Admin — Memory
  // =========================================================================

  http.get(`${ADMIN_BASE}/memory/records`, ({ request }) => {
    const url = new URL(request.url);
    const page = parseInt(url.searchParams.get("page") ?? "1", 10);
    const perPage = parseInt(url.searchParams.get("per_page") ?? "20", 10);
    const persona = url.searchParams.get("persona");
    const dimension = url.searchParams.get("dimension");
    const category = url.searchParams.get("category");
    const status = url.searchParams.get("status");
    const source = url.searchParams.get("source");
    const createdBy = url.searchParams.get("created_by");
    const entityUrn = url.searchParams.get("entity_urn");

    let filtered = [...mockMemoryRecords];
    if (persona) filtered = filtered.filter((r) => r.persona === persona);
    if (dimension) filtered = filtered.filter((r) => r.dimension === dimension);
    if (category) filtered = filtered.filter((r) => r.category === category);
    if (status) filtered = filtered.filter((r) => r.status === status);
    if (source) filtered = filtered.filter((r) => r.source === source);
    if (createdBy)
      filtered = filtered.filter((r) => r.created_by === createdBy);
    if (entityUrn)
      filtered = filtered.filter((r) => r.entity_urns.includes(entityUrn));

    const start = (page - 1) * perPage;
    const data = filtered.slice(start, start + perPage);
    return HttpResponse.json({
      data,
      total: filtered.length,
      page,
      per_page: perPage,
    });
  }),

  http.get(`${ADMIN_BASE}/memory/records/stats`, () => {
    return HttpResponse.json(mockMemoryStats);
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
];
