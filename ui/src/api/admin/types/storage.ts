// Storage capacity (#2148), as /api/v1/admin/storage and
// /api/v1/admin/settings/storage return it.

/** StoreKind is what a store is. */
export type StoreKind = "database" | "seaweedfs" | "bucket" | "spool";

/** StoreView is one store: used and free, its growth and when it fills. */
export interface StoreView {
  /** The key its history is kept under: database, seaweedfs, bucket:<name>, spool:<replica>. */
  store: string;
  kind: StoreKind;
  name: string;
  used_bytes: number;
  /** Capacity; null where the platform knows none. */
  total_bytes: number | null;
  /** Where a bucket's capacity comes from: its budget, or the SeaweedFS cluster's free space. */
  capacity_from?: "budget" | "seaweedfs";
  free_bytes: number | null;
  percent_used: number | null;
  /** Growth in bytes per day over the last 7 and 30 days; null when the history is too short. */
  rate_7d_bytes_per_day: number | null;
  rate_30d_bytes_per_day: number | null;
  /** The rate the projection uses: the slower growing one. */
  rate_used?: "7d" | "30d";
  days_left: number | null;
  exhaustion_at: string | null;
  level?: "warning" | "critical";
}

export interface StorageTable {
  name: string;
  index: boolean;
  bytes: number;
  rows: number;
}

export interface StorageBucketUsage {
  bucket: string;
  purpose: string;
  bytes: number;
  objects: number;
  listed_at: string | null;
}

export interface StorageReconcile {
  purpose: string;
  orphaned: number;
  dangling: number;
  reconciled_at: string;
}

export interface StorageSpool {
  replica: string;
  dir: string;
  bytes: number;
  max_bytes: number;
  fs_total_bytes: number | null;
  fs_free_bytes: number | null;
  reporting: boolean;
  updated_at: string;
}

export interface StorageReport {
  generated_at: string;
  history_days: number;
  stores: StoreView[];
  database: { bytes: number; limit_bytes: number | null; tables: StorageTable[] };
  buckets: StorageBucketUsage[];
  reconciles: StorageReconcile[];
  last_scan: { duration_seconds: number; objects: number; finished_at: string } | null;
  seaweedfs: {
    master_url: string;
    capacity: { total_bytes: number; used_bytes: number; free_bytes: number; servers: number; unreached: string[] };
    error?: string;
  } | null;
  /** The buckets whose endpoint answers as SeaweedFS; empty when none does. */
  seaweedfs_buckets: string[];
  spools: StorageSpool[];
}

export interface StoragePoint {
  at: string;
  used_bytes: number;
  total_bytes: number | null;
}

export interface StorageHistory {
  store: string;
  days: number;
  points: StoragePoint[];
}

/** Thresholds are a warning and a critical value; 0 is off. */
export interface Thresholds {
  warning: number;
  critical: number;
}

export interface StorageSettings {
  channels: string[];
  database_limit_bytes: number;
  budgets: Record<string, number>;
  seaweedfs: { master_url: string; volume_urls: string[] };
  history_days: number;
  used_percent: Thresholds;
  days_left: Thresholds;
  updated_by?: string;
  updated_at?: string;
}

export type StorageSettingsInput = Partial<Omit<StorageSettings, "updated_by" | "updated_at">>;
