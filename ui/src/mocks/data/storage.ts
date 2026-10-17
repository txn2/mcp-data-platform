import type { StorageHistory, StorageReport, StorageSettings, StoreView } from "@/api/admin/types";

// Storage capacity for the demo portal and the page tests (#2148): a database
// against its limit, a SeaweedFS cluster read from its master, a cloud bucket
// against a budget and filling within a month, and two webhook spools, one on
// a nearly full volume.

const GIB = 1024 ** 3;
const DAY = 24 * 3600_000;

function daysFromNow(n: number): string {
  return new Date(Date.now() + n * DAY).toISOString();
}

function store(s: Omit<StoreView, "free_bytes" | "percent_used"> & Partial<StoreView>): StoreView {
  const total = s.total_bytes;
  return {
    free_bytes: total == null ? null : Math.max(0, total - s.used_bytes),
    percent_used: total == null ? null : (100 * s.used_bytes) / total,
    ...s,
  };
}

export const mockStorageSettings: StorageSettings = {
  channels: ["ops-alerts"],
  database_limit_bytes: 100 * GIB,
  budgets: { "acme-portal-assets": 500 * GIB },
  seaweedfs: { master_url: "http://seaweedfs-master:9333", volume_urls: [] },
  history_days: 180,
  used_percent: { warning: 75, critical: 90 },
  days_left: { warning: 30, critical: 7 },
  updated_by: "sarah.chen@example.com",
  updated_at: "2026-10-12T10:00:00Z",
};

export function mockStorageReport(): StorageReport {
  return {
    generated_at: new Date().toISOString(),
    history_days: mockStorageSettings.history_days,
    stores: [
      store({
        store: "database", kind: "database", name: "Database", used_bytes: 41 * GIB, total_bytes: 100 * GIB,
        rate_7d_bytes_per_day: 0.4 * GIB, rate_30d_bytes_per_day: 0.35 * GIB, rate_used: "30d",
        days_left: 168, exhaustion_at: daysFromNow(168),
      }),
      store({
        store: "seaweedfs", kind: "seaweedfs", name: "SeaweedFS cluster", used_bytes: 1.62 * 1024 * GIB,
        total_bytes: 2 * 1024 * GIB, rate_7d_bytes_per_day: 9 * GIB, rate_30d_bytes_per_day: 7.5 * GIB,
        rate_used: "30d", days_left: 52, exhaustion_at: daysFromNow(52), level: "warning",
      }),
      store({
        store: "bucket:acme-portal-assets", kind: "bucket", name: "Bucket acme-portal-assets", used_bytes: 462 * GIB,
        total_bytes: 500 * GIB, capacity_from: "budget", rate_7d_bytes_per_day: 2.2 * GIB, rate_30d_bytes_per_day: 1.6 * GIB, rate_used: "30d",
        days_left: 23.7, exhaustion_at: daysFromNow(23.7), level: "critical",
      }),
      store({
        store: "bucket:acme-resources", kind: "bucket", name: "Bucket acme-resources", used_bytes: 88 * GIB,
        total_bytes: null, rate_7d_bytes_per_day: 0.2 * GIB, rate_30d_bytes_per_day: null, rate_used: "7d",
        days_left: null, exhaustion_at: null,
      }),
      store({
        store: "spool:mcp-data-platform-0", kind: "spool", name: "Webhook spool on mcp-data-platform-0",
        used_bytes: 1.1 * GIB, total_bytes: 10 * GIB, rate_7d_bytes_per_day: null, rate_30d_bytes_per_day: null,
        days_left: null, exhaustion_at: null,
      }),
      store({
        store: "spool:mcp-data-platform-1", kind: "spool", name: "Webhook spool on mcp-data-platform-1",
        used_bytes: 9.3 * GIB, total_bytes: 10 * GIB, rate_7d_bytes_per_day: 0.5 * GIB, rate_30d_bytes_per_day: 0.1 * GIB,
        rate_used: "30d", days_left: 7, exhaustion_at: daysFromNow(7), level: "critical",
      }),
    ],
    database: {
      bytes: 41 * GIB,
      limit_bytes: 100 * GIB,
      tables: [
        { name: "audit_logs", index: false, bytes: 18 * GIB, rows: 92_000_000 },
        { name: "idx_memory_records_embedding_hnsw", index: true, bytes: 3.2 * GIB, rows: 0 },
        { name: "call_records", index: false, bytes: 2.1 * GIB, rows: 4_400_000 },
      ],
    },
    buckets: [
      { bucket: "acme-portal-assets", purpose: "portal_assets", bytes: 410 * GIB, objects: 182_000, listed_at: daysFromNow(-0.2) },
      { bucket: "acme-portal-assets", purpose: "thumbnails", bytes: 52 * GIB, objects: 364_000, listed_at: daysFromNow(-0.2) },
      { bucket: "acme-resources", purpose: "webhooks", bytes: 61 * GIB, objects: 1_210_000, listed_at: daysFromNow(-0.2) },
      { bucket: "acme-resources", purpose: "resources", bytes: 27 * GIB, objects: 9_400, listed_at: null },
    ],
    reconciles: [
      { purpose: "portal_assets", orphaned: 12, dangling: 0, reconciled_at: daysFromNow(-0.2) },
      { purpose: "resources", orphaned: 0, dangling: 2, reconciled_at: daysFromNow(-0.2) },
    ],
    last_scan: { duration_seconds: 412.5, objects: 1_765_400, finished_at: daysFromNow(-0.2) },
    seaweedfs: {
      master_url: "http://seaweedfs-master:9333",
      capacity: { total_bytes: 2 * 1024 * GIB, used_bytes: 1.62 * 1024 * GIB, free_bytes: 0.38 * 1024 * GIB, servers: 3, unreached: [] },
    },
    spools: [
      {
        replica: "mcp-data-platform-0", dir: "/var/lib/mcp-data-platform/spool", bytes: 182_400, max_bytes: GIB,
        fs_total_bytes: 10 * GIB, fs_free_bytes: 8.9 * GIB, reporting: true, updated_at: daysFromNow(-0.0001),
      },
      {
        replica: "mcp-data-platform-1", dir: "/var/lib/mcp-data-platform/spool", bytes: 9_830_400, max_bytes: GIB,
        fs_total_bytes: 10 * GIB, fs_free_bytes: 0.7 * GIB, reporting: true, updated_at: daysFromNow(-0.0001),
      },
    ],
  };
}

/** mockStorageHistory is a store's samples every six hours over the days
 * asked, growing toward its current use. */
export function mockStorageHistory(storeKey: string, days: number): StorageHistory {
  const s = mockStorageReport().stores.find((x) => x.store === storeKey);
  if (!s) return { store: storeKey, days, points: [] };
  const rate = s.rate_30d_bytes_per_day ?? s.rate_7d_bytes_per_day ?? 0;
  const points = [];
  const step = 6 * 3600_000;
  const now = Date.now();
  for (let t = now - days * DAY; t <= now; t += step) {
    const back = (now - t) / DAY;
    points.push({ at: new Date(t).toISOString(), used_bytes: Math.max(0, Math.round(s.used_bytes - rate * back)), total_bytes: s.total_bytes });
  }
  return { store: storeKey, days, points };
}
