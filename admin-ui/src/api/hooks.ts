import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { apiFetch } from "./client";
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
  InsightListResponse,
  InsightStats,
  ChangesetListResponse,
  ToolSchemaMap,
  ToolCallRequest,
  ToolCallResponse,
  PersonaListResponse,
  PersonaDetail,
  PersonaCreateRequest,
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
  search?: string;
  sortBy?: AuditSortColumn;
  sortOrder?: SortOrder;
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

// ---------------------------------------------------------------------------
// Knowledge — Insights & Changesets
// ---------------------------------------------------------------------------

interface InsightsParams {
  page?: number;
  perPage?: number;
  status?: string;
  category?: string;
  confidence?: string;
  entityUrn?: string;
  capturedBy?: string;
}

export function useInsights(params: InsightsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.perPage) searchParams.set("per_page", String(params.perPage));
  if (params.status) searchParams.set("status", params.status);
  if (params.category) searchParams.set("category", params.category);
  if (params.confidence) searchParams.set("confidence", params.confidence);
  if (params.entityUrn) searchParams.set("entity_urn", params.entityUrn);
  if (params.capturedBy) searchParams.set("captured_by", params.capturedBy);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["knowledge", "insights", params],
    queryFn: () =>
      apiFetch<InsightListResponse>(
        `/knowledge/insights${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}

export function useInsightStats() {
  return useQuery({
    queryKey: ["knowledge", "insights", "stats"],
    queryFn: () => apiFetch<InsightStats>("/knowledge/insights/stats"),
    refetchInterval: REFETCH_INTERVAL,
  });
}

export function useUpdateInsightStatus() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      status,
      reviewNotes,
    }: {
      id: string;
      status: string;
      reviewNotes?: string;
    }) =>
      apiFetch(`/knowledge/insights/${id}/status`, {
        method: "PUT",
        body: JSON.stringify({ status, review_notes: reviewNotes }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["knowledge"] });
    },
  });
}

interface ChangesetsParams {
  page?: number;
  perPage?: number;
  entityUrn?: string;
  appliedBy?: string;
  rolledBack?: string;
}

export function useChangesets(params: ChangesetsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.perPage) searchParams.set("per_page", String(params.perPage));
  if (params.entityUrn) searchParams.set("entity_urn", params.entityUrn);
  if (params.appliedBy) searchParams.set("applied_by", params.appliedBy);
  if (params.rolledBack) searchParams.set("rolled_back", params.rolledBack);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["knowledge", "changesets", params],
    queryFn: () =>
      apiFetch<ChangesetListResponse>(
        `/knowledge/changesets${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}

export function useRollbackChangeset() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/knowledge/changesets/${id}/rollback`, {
        method: "POST",
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["knowledge"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Tools — Schema & Execution
// ---------------------------------------------------------------------------

export function useToolSchemas() {
  return useQuery({
    queryKey: ["tools", "schemas"],
    queryFn: () => apiFetch<ToolSchemaMap>("/tools/schemas"),
    staleTime: 5 * 60_000,
  });
}

export function useCallTool() {
  return useMutation({
    mutationFn: (req: ToolCallRequest) =>
      apiFetch<ToolCallResponse>("/tools/call", {
        method: "POST",
        body: JSON.stringify(req),
      }),
  });
}

// ---------------------------------------------------------------------------
// Personas
// ---------------------------------------------------------------------------

export function usePersonas() {
  return useQuery({
    queryKey: ["personas"],
    queryFn: () => apiFetch<PersonaListResponse>("/personas"),
  });
}

export function usePersonaDetail(name: string | null) {
  return useQuery({
    queryKey: ["personas", name],
    queryFn: () => apiFetch<PersonaDetail>(`/personas/${name}`),
    enabled: !!name,
  });
}

export function useCreatePersona() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: PersonaCreateRequest) =>
      apiFetch<PersonaDetail>("/personas", {
        method: "POST",
        body: JSON.stringify(req),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["personas"] });
    },
  });
}

export function useUpdatePersona() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, ...body }: PersonaCreateRequest) =>
      apiFetch<PersonaDetail>(`/personas/${name}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["personas"] });
    },
  });
}

export function useDeletePersona() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch(`/personas/${name}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["personas"] });
    },
  });
}

