import { useMemo } from "react";
import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  SystemInfo,
  ToolListResponse,
  ConnectionListResponse,
  AuditEventResponse,
  AuditFiltersResponse,
  AuditSortColumn,
  SortOrder,
  TimeseriesBucket,
  BreakdownEntry,
  Overview,
  PerformanceStats,
  Resolution,
  BreakdownDimension,
} from "../types";
import { REFETCH_INTERVAL } from "./shared";

export function useSystemInfo(enabled = true) {
  return useQuery({
    queryKey: ["system", "info"],
    queryFn: () => apiFetch<SystemInfo>("/system/info"),
    refetchInterval: REFETCH_INTERVAL,
    enabled,
  });
}

export function useTools() {
  return useQuery({
    queryKey: ["tools"],
    queryFn: () => apiFetch<ToolListResponse>("/tools"),
  });
}

/** Returns a stable map of tool name → title from the tools list. */
export function useToolTitleMap(): Record<string, string> {
  const { data } = useTools();
  return useMemo(() => {
    const map: Record<string, string> = {};
    for (const t of data?.tools ?? []) {
      if (t.title) map[t.name] = t.title;
    }
    return map;
  }, [data]);
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
  toolkitKind?: string;
  source?: string;
  search?: string;
  sortBy?: AuditSortColumn;
  sortOrder?: SortOrder;
  success?: boolean | null;
  eventKind?: string;
  startTime?: string;
  endTime?: string;
}

export function useAuditEvents(params: AuditEventsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.perPage) searchParams.set("per_page", String(params.perPage));
  if (params.userId) searchParams.set("user_id", params.userId);
  if (params.toolName) searchParams.set("tool_name", params.toolName);
  if (params.toolkitKind) searchParams.set("toolkit_kind", params.toolkitKind);
  if (params.source) searchParams.set("source", params.source);
  if (params.eventKind) searchParams.set("event_kind", params.eventKind);
  if (params.search) searchParams.set("search", params.search);
  if (params.sortBy) searchParams.set("sort_by", params.sortBy);
  if (params.sortOrder) searchParams.set("sort_order", params.sortOrder);
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
    placeholderData: keepPreviousData,
  });
}

export function useAuditFilters() {
  return useQuery({
    queryKey: ["audit", "filters"],
    queryFn: () => apiFetch<AuditFiltersResponse>("/audit/events/filters"),
    staleTime: 60_000,
  });
}

interface TimeseriesParams {
  resolution?: Resolution;
  eventKind?: string;
  startTime?: string;
  endTime?: string;
}

export function useAuditTimeseries(params: TimeseriesParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.resolution)
    searchParams.set("resolution", params.resolution);
  if (params.eventKind) searchParams.set("event_kind", params.eventKind);
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
  eventKind?: string;
  startTime?: string;
  endTime?: string;
}

export function useAuditBreakdown(params: BreakdownParams) {
  const searchParams = new URLSearchParams();
  searchParams.set("group_by", params.groupBy);
  if (params.limit) searchParams.set("limit", String(params.limit));
  if (params.eventKind) searchParams.set("event_kind", params.eventKind);
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
  eventKind?: string;
  startTime?: string;
  endTime?: string;
}

export function useAuditOverview(params: TimeRangeParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.eventKind) searchParams.set("event_kind", params.eventKind);
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
  if (params.eventKind) searchParams.set("event_kind", params.eventKind);
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
