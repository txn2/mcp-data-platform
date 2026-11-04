import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// ---------------------------------------------------------------------------
// Operating an application database (kind=appdb, #2406, #2409, #2410, #2427):
// health and its history, drift and adopting an existing schema, maintenance,
// cancelling and terminating a backend, the console, and the health settings.
// ---------------------------------------------------------------------------

const base = (name: string) => `/connection-instances/appdb/${encodeURIComponent(name)}`;

export interface AppDBTableStats {
  schema: string;
  name: string;
  size_bytes: number;
  row_estimate: number;
  live_tuples: number;
  dead_tuples: number;
  dead_ratio: number;
  seq_scans: number;
  index_scans: number;
  last_vacuum?: string;
  last_autovacuum?: string;
  last_analyze?: string;
  last_autoanalyze?: string;
  transaction_id_age: number;
}

export interface AppDBIndexRef {
  schema: string;
  table: string;
  name: string;
  size_bytes: number;
  scans: number;
}

export interface AppDBSession {
  pid: number;
  user: string;
  application: string;
  state: string;
  seconds: number;
  state_seconds: number;
  wait?: string;
  query: string;
}

export interface AppDBHealthReport {
  connection: string;
  generated_at: string;
  server: {
    version: string;
    version_num: number;
    database: string;
    size_bytes: number;
    transaction_id_age: number;
    cache_hit_ratio: number;
    in_recovery: boolean;
    stats_reset?: string;
  };
  extensions: { name: string; version: string }[];
  connections: { max: number; total: number; by_state: Record<string, number> };
  tables: AppDBTableStats[];
  indexes: {
    unused: AppDBIndexRef[];
    duplicate: { schema: string; table: string; names: string[]; definition: string }[];
    invalid: AppDBIndexRef[];
  };
  sessions: AppDBSession[];
  blocking: { pid: number; user: string; blocked_by: number[]; seconds: number }[];
  replication: { name: string; state: string; lag_bytes: number; lag_seconds: number }[];
  statements: {
    installed: boolean;
    note?: string;
    top: { query_id: string; calls: number; total_ms: number; mean_ms: number; rows: number; query: string }[];
  };
  errors: { section: string; error: string }[];
}

export interface AppDBDifference {
  kind: string;
  object: string;
  change: "added" | "missing" | "changed";
  live?: string;
  expected?: string;
}

export interface AppDBDriftResult {
  connection: string;
  schema: string;
  checked_at: string;
  checked: boolean;
  reason?: string;
  applied_version: number;
  differences: AppDBDifference[];
}

export interface AppDBHealthPoint {
  connection: string;
  sampled_at: string;
  sample: {
    size_bytes: number;
    transaction_id_age: number;
    connections: number;
    max_connections: number;
    idle_in_transaction: number;
    longest_idle_seconds: number;
    long_running: number;
    blocked: number;
    max_dead_ratio: number;
    reachable: boolean;
    drift_objects: number;
    drift_unchecked: number;
  };
}

export interface AppDBAdoptResult {
  connection: string;
  schema: string;
  adopted: boolean;
  version: number;
  checksum?: string;
  up?: string;
  unsupported: { kind: string; name: string }[];
  differences: AppDBDifference[];
}

export interface AppDBMaintenanceOutcome {
  connection: string;
  command: string;
  table?: string;
  statement: string;
  duration_ms: number;
}

export interface AppDBConsoleResult {
  connection: string;
  read_only: boolean;
  committed: boolean;
  results: {
    index: number;
    command: string;
    rows_affected: number;
    columns: { name: string; type: string }[];
    rows: unknown[][];
    truncated: boolean;
  }[];
  error?: { index: number; code: string; summary: string; detail?: string };
}

export interface AppDBThresholds {
  warning: number;
  critical: number;
}

export interface AppDBHealthSettings {
  channels: string[];
  interval_seconds: number;
  history_days: number;
  long_running_seconds: number;
  transaction_id_age: AppDBThresholds;
  idle_in_transaction_seconds: AppDBThresholds;
  connections_percent: AppDBThresholds;
  invalid_indexes: AppDBThresholds;
  dead_tuple_percent: AppDBThresholds;
  drift_objects: AppDBThresholds;
  updated_by?: string;
  updated_at?: string;
}

export function useAppDBHealth(name: string) {
  return useQuery({
    queryKey: ["appdb-health", name],
    queryFn: () => apiFetch<AppDBHealthReport>(`${base(name)}/health`),
    enabled: !!name,
  });
}

export function useAppDBHealthHistory(name: string, days: number) {
  return useQuery({
    queryKey: ["appdb-health-history", name, days],
    queryFn: () =>
      apiFetch<{ connection: string; days: number; points: AppDBHealthPoint[] }>(
        `${base(name)}/health/history?days=${days}`,
      ),
    enabled: !!name,
  });
}

export function useAppDBDrift(name: string) {
  return useQuery({
    queryKey: ["appdb-drift", name],
    queryFn: () => apiFetch<{ connection: string; schemas: AppDBDriftResult[] }>(`${base(name)}/drift`),
    enabled: !!name,
  });
}

export function useAdoptAppDBSchema(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (schema: string) =>
      apiFetch<AppDBAdoptResult>(`${base(name)}/adopt?schema=${encodeURIComponent(schema)}`, { method: "POST" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["appdb-drift", name] });
      void qc.invalidateQueries({ queryKey: ["appdb-migrations", name] });
    },
  });
}

export function useAppDBMaintenance(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: { command: string; table?: string }) =>
      apiFetch<AppDBMaintenanceOutcome>(`${base(name)}/maintenance`, {
        method: "POST",
        body: JSON.stringify(req),
      }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["appdb-health", name] }),
  });
}

export function useAppDBSignal(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: { pid: number; signal: "cancel" | "terminate" }) =>
      apiFetch<{ connection: string; signal: string; pid: number; sent: boolean }>(
        `${base(name)}/backends/${req.pid}/${req.signal}`,
        { method: "POST" },
      ),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["appdb-health", name] }),
  });
}

export function useAppDBConsole(name: string) {
  return useMutation({
    mutationFn: (req: { sql: string; read_only: boolean }) =>
      apiFetch<AppDBConsoleResult>(`${base(name)}/console`, { method: "POST", body: JSON.stringify(req) }),
  });
}

export function useAppDBHealthSettings() {
  return useQuery({
    queryKey: ["appdb-health-settings"],
    queryFn: () => apiFetch<AppDBHealthSettings>("/appdb-health/settings"),
  });
}

export function useSaveAppDBHealthSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (s: AppDBHealthSettings) =>
      apiFetch<AppDBHealthSettings>("/appdb-health/settings", { method: "PUT", body: JSON.stringify(s) }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["appdb-health-settings"] }),
  });
}
