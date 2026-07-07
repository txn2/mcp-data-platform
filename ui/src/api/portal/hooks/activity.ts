import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  PaginatedResponse,
  ActivityOverview,
  TimeseriesBucket,
  BreakdownEntry,
  Insight,
  InsightStats,
  ScoredInsight,
  MemoryRecord,
  MemoryStats,
  ScoredMemoryRecord,
} from "../types";

// --- Activity (user-scoped audit metrics) ---

export function useMyActivityOverview(params?: {
  startTime?: string;
  endTime?: string;
}) {
  const sp = new URLSearchParams();
  if (params?.startTime) sp.set("start_time", params.startTime);
  if (params?.endTime) sp.set("end_time", params.endTime);
  const qs = sp.toString();

  return useQuery({
    queryKey: ["my-activity-overview", params],
    queryFn: () =>
      apiFetch<ActivityOverview>(
        `/activity/overview${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: 30_000,
  });
}

export function useMyActivityTimeseries(params?: {
  resolution?: string;
  startTime?: string;
  endTime?: string;
}) {
  const sp = new URLSearchParams();
  if (params?.resolution) sp.set("resolution", params.resolution);
  if (params?.startTime) sp.set("start_time", params.startTime);
  if (params?.endTime) sp.set("end_time", params.endTime);
  const qs = sp.toString();

  return useQuery({
    queryKey: ["my-activity-timeseries", params],
    queryFn: () =>
      apiFetch<TimeseriesBucket[]>(
        `/activity/timeseries${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: 30_000,
  });
}

export function useMyActivityBreakdown(params?: {
  groupBy?: string;
  limit?: number;
  startTime?: string;
  endTime?: string;
}) {
  const sp = new URLSearchParams();
  if (params?.groupBy) sp.set("group_by", params.groupBy);
  if (params?.limit) sp.set("limit", String(params.limit));
  if (params?.startTime) sp.set("start_time", params.startTime);
  if (params?.endTime) sp.set("end_time", params.endTime);
  const qs = sp.toString();

  return useQuery({
    queryKey: ["my-activity-breakdown", params],
    queryFn: () =>
      apiFetch<BreakdownEntry[]>(
        `/activity/breakdown${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: 30_000,
  });
}

// --- Knowledge (user-scoped insights) ---

export function useMyInsights(params?: {
  status?: string;
  category?: string;
  limit?: number;
  offset?: number;
}) {
  const sp = new URLSearchParams();
  if (params?.status) sp.set("status", params.status);
  if (params?.category) sp.set("category", params.category);
  if (params?.limit) sp.set("limit", String(params.limit));
  if (params?.offset) sp.set("offset", String(params.offset));
  const qs = sp.toString();

  return useQuery({
    queryKey: ["my-insights", params],
    queryFn: () =>
      apiFetch<PaginatedResponse<Insight>>(
        `/knowledge/insights${qs ? `?${qs}` : ""}`,
      ),
  });
}

export function useMyInsightStats() {
  return useQuery({
    queryKey: ["my-insight-stats"],
    queryFn: () => apiFetch<InsightStats>("/knowledge/insights/stats"),
  });
}

// useSearchMyInsights ranks the caller's insights by relevance to query.
// Disabled (no request) until query is non-empty, so the list endpoint
// remains the default browse experience.
export function useSearchMyInsights(
  query: string,
  params?: { status?: string; limit?: number },
) {
  const q = query.trim();
  const sp = new URLSearchParams({ q });
  if (params?.status) sp.set("status", params.status);
  if (params?.limit) sp.set("limit", String(params.limit));

  return useQuery({
    queryKey: ["search-my-insights", q, params],
    enabled: q.length > 0,
    queryFn: () =>
      apiFetch<PaginatedResponse<ScoredInsight>>(
        `/knowledge/insights/search?${sp.toString()}`,
      ),
  });
}

// --- Memory (user-scoped memory records) ---

export function useMyMemories(params?: {
  dimension?: string;
  sinkClass?: string;
  category?: string;
  status?: string;
  source?: string;
  limit?: number;
  offset?: number;
}) {
  const sp = new URLSearchParams();
  if (params?.dimension) sp.set("dimension", params.dimension);
  if (params?.sinkClass) sp.set("sink_class", params.sinkClass);
  if (params?.category) sp.set("category", params.category);
  if (params?.status) sp.set("status", params.status);
  if (params?.source) sp.set("source", params.source);
  if (params?.limit) sp.set("limit", String(params.limit));
  if (params?.offset) sp.set("offset", String(params.offset));
  const qs = sp.toString();

  return useQuery({
    queryKey: ["my-memories", params],
    queryFn: () =>
      apiFetch<PaginatedResponse<MemoryRecord>>(
        `/memory/records${qs ? `?${qs}` : ""}`,
      ),
  });
}

export function useMyMemoryStats() {
  return useQuery({
    queryKey: ["my-memory-stats"],
    queryFn: () => apiFetch<MemoryStats>("/memory/records/stats"),
  });
}

// useSearchMyMemories ranks the caller's memory records by relevance to
// query. Disabled (no request) until query is non-empty, so the list
// endpoint remains the default browse experience.
export function useSearchMyMemories(
  query: string,
  params?: { dimension?: string; status?: string; limit?: number },
) {
  const q = query.trim();
  const sp = new URLSearchParams({ q });
  if (params?.dimension) sp.set("dimension", params.dimension);
  if (params?.status) sp.set("status", params.status);
  if (params?.limit) sp.set("limit", String(params.limit));

  return useQuery({
    queryKey: ["search-my-memories", q, params],
    enabled: q.length > 0,
    queryFn: () =>
      apiFetch<PaginatedResponse<ScoredMemoryRecord>>(
        `/memory/records/search?${sp.toString()}`,
      ),
  });
}
