import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "./client";
import type {
  SystemInfo,
  ToolListResponse,
  ConnectionListResponse,
  AuditEventResponse,
  TimeseriesBucket,
  BreakdownEntry,
  Overview,
  PerformanceStats,
  Resolution,
  BreakdownDimension,
} from "./types";

// Refresh interval for auto-updating queries (30 seconds)
const REFETCH_INTERVAL = 30_000;

export function useSystemInfo() {
  return useQuery({
    queryKey: ["system", "info"],
    queryFn: () => apiFetch<SystemInfo>("/system/info"),
    refetchInterval: REFETCH_INTERVAL,
  });
}

export function useTools() {
  return useQuery({
    queryKey: ["tools"],
    queryFn: () => apiFetch<ToolListResponse>("/tools"),
  });
}

export function useConnections() {
  return useQuery({
    queryKey: ["connections"],
    queryFn: () => apiFetch<ConnectionListResponse>("/connections"),
    refetchInterval: REFETCH_INTERVAL,
  });
}

interface AuditEventsParams {
  page?: number;
  perPage?: number;
  userId?: string;
  toolName?: string;
  success?: boolean | null;
  startTime?: string;
  endTime?: string;
}

export function useAuditEvents(params: AuditEventsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.perPage) searchParams.set("per_page", String(params.perPage));
  if (params.userId) searchParams.set("user_id", params.userId);
  if (params.toolName) searchParams.set("tool_name", params.toolName);
  if (params.success !== null && params.success !== undefined)
    searchParams.set("success", String(params.success));
  if (params.startTime) searchParams.set("start_time", params.startTime);
  if (params.endTime) searchParams.set("end_time", params.endTime);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["audit", "events", params],
    queryFn: () =>
      apiFetch<AuditEventResponse>(`/audit/events${qs ? `?${qs}` : ""}`),
    refetchInterval: REFETCH_INTERVAL,
  });
}

interface TimeseriesParams {
  resolution?: Resolution;
  startTime?: string;
  endTime?: string;
}

export function useAuditTimeseries(params: TimeseriesParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.resolution)
    searchParams.set("resolution", params.resolution);
  if (params.startTime) searchParams.set("start_time", params.startTime);
  if (params.endTime) searchParams.set("end_time", params.endTime);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["audit", "metrics", "timeseries", params],
    queryFn: () =>
      apiFetch<TimeseriesBucket[]>(
        `/audit/metrics/timeseries${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
  });
}

interface BreakdownParams {
  groupBy: BreakdownDimension;
  limit?: number;
  startTime?: string;
  endTime?: string;
}

export function useAuditBreakdown(params: BreakdownParams) {
  const searchParams = new URLSearchParams();
  searchParams.set("group_by", params.groupBy);
  if (params.limit) searchParams.set("limit", String(params.limit));
  if (params.startTime) searchParams.set("start_time", params.startTime);
  if (params.endTime) searchParams.set("end_time", params.endTime);

  return useQuery({
    queryKey: ["audit", "metrics", "breakdown", params],
    queryFn: () =>
      apiFetch<BreakdownEntry[]>(
        `/audit/metrics/breakdown?${searchParams.toString()}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
  });
}

interface TimeRangeParams {
  startTime?: string;
  endTime?: string;
}

export function useAuditOverview(params: TimeRangeParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.startTime) searchParams.set("start_time", params.startTime);
  if (params.endTime) searchParams.set("end_time", params.endTime);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["audit", "metrics", "overview", params],
    queryFn: () =>
      apiFetch<Overview>(`/audit/metrics/overview${qs ? `?${qs}` : ""}`),
    refetchInterval: REFETCH_INTERVAL,
  });
}

export function useAuditPerformance(params: TimeRangeParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.startTime) searchParams.set("start_time", params.startTime);
  if (params.endTime) searchParams.set("end_time", params.endTime);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["audit", "metrics", "performance", params],
    queryFn: () =>
      apiFetch<PerformanceStats>(
        `/audit/metrics/performance${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
  });
}
