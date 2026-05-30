import { useMemo } from "react";
import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "./client";
import type {
  SystemInfo,
  ToolListResponse,
  ConnectionListResponse,
  ConnectionInstance,
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
  ToolDetail,
  ToolVisibilityRequest,
  ToolVisibilityResponse,
  PersonaListResponse,
  PersonaDetail,
  PersonaCreateRequest,
  PersonaTestAccessRequest,
  PersonaTestAccessResult,
  AdminAssetListResponse,
  PromptListResponse,
  Prompt,
  MemoryListResponse,
  MemoryStats,
} from "./types";
import type { Asset, AssetVersion, PaginatedResponse } from "@/api/portal/types";

// Refresh interval for auto-updating queries (30 seconds)
const REFETCH_INTERVAL = 30_000;

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

// Aggregating per-tool detail used by the Tools master-detail page.
export function useToolDetail(name: string | null) {
  return useQuery({
    queryKey: ["tools", "detail", name],
    queryFn: () => apiFetch<ToolDetail>(`/tools/${encodeURIComponent(name!)}`),
    enabled: !!name,
  });
}

// Save a per-tool description override. Empty value reverts to default.
export function useUpdateToolDescription(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (value: string) =>
      apiFetch(
        `/config/entries/${encodeURIComponent(`tool.${name}.description`)}`,
        {
          method: "PUT",
          body: JSON.stringify({ value }),
        },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tools", "detail", name] });
      queryClient.invalidateQueries({ queryKey: ["tools"] });
    },
  });
}

// Remove an existing description override (revert to default).
export function useResetToolDescription(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch(
        `/config/entries/${encodeURIComponent(`tool.${name}.description`)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tools", "detail", name] });
      queryClient.invalidateQueries({ queryKey: ["tools"] });
    },
  });
}

// Toggle a tool's membership in the platform-wide tools.deny list.
export function useSetToolVisibility(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: ToolVisibilityRequest) =>
      apiFetch<ToolVisibilityResponse>(
        `/tools/${encodeURIComponent(name)}/visibility`,
        {
          method: "PUT",
          body: JSON.stringify(req),
        },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tools", "detail", name] });
      queryClient.invalidateQueries({ queryKey: ["tools"] });
      // Other admin surfaces still derive hidden state from /connections.
      queryClient.invalidateQueries({ queryKey: ["connections"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Personas
// ---------------------------------------------------------------------------

export function usePersonas(enabled = true) {
  return useQuery({
    queryKey: ["personas"],
    queryFn: () => apiFetch<PersonaListResponse>("/personas"),
    enabled,
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

// Preview a persona's allow/deny decision for a single tool name without
// editing persona rules. Returns matched pattern + source.
export function useTestPersonaAccess() {
  return useMutation({
    mutationFn: ({
      persona,
      toolName,
    }: {
      persona: string;
      toolName: string;
    }) =>
      apiFetch<PersonaTestAccessResult>(
        `/personas/${encodeURIComponent(persona)}/test-access`,
        {
          method: "POST",
          body: JSON.stringify({ tool_name: toolName } as PersonaTestAccessRequest),
        },
      ),
  });
}

// ---------------------------------------------------------------------------
// Assets (admin-scoped)
// ---------------------------------------------------------------------------

interface AdminAssetsParams {
  search?: string;
  limit?: number;
  offset?: number;
}

export function useAdminAssets(params: AdminAssetsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.search) searchParams.set("search", params.search);
  if (params.limit) searchParams.set("limit", String(params.limit));
  if (params.offset) searchParams.set("offset", String(params.offset));

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["admin", "assets", params],
    queryFn: () =>
      apiFetch<AdminAssetListResponse>(
        `/assets${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}

export function useAdminAsset(id: string | null) {
  return useQuery({
    queryKey: ["admin", "asset", id],
    queryFn: () => apiFetch<Asset>(`/assets/${id}`),
    enabled: !!id,
  });
}

/** Maximum asset size for auto-loading content in the admin viewer (matches portal threshold). */
const ADMIN_LARGE_ASSET_THRESHOLD = 2 * 1024 * 1024; // 2 MB

export function useAdminAssetContent(id: string | null, sizeBytes?: number) {
  const tooLarge = sizeBytes != null && sizeBytes > ADMIN_LARGE_ASSET_THRESHOLD;
  return useQuery({
    queryKey: ["admin", "asset-content", id],
    queryFn: async () => {
      const res = await fetch(`/api/v1/admin/assets/${id}/content`, {
        credentials: "include",
      });
      if (!res.ok) throw new Error("Failed to fetch content");
      return res.text();
    },
    enabled: !!id && !tooLarge,
  });
}

export function useAdminUpdateAsset() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: string;
      name?: string;
      description?: string;
      tags?: string[];
    }) =>
      apiFetch(`/assets/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "assets"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset"] });
    },
  });
}

export function useAdminDeleteAsset() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/assets/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "assets"] });
    },
  });
}

export function useAdminUpdateAssetContent() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, content, changeSummary }: { id: string; content: string; changeSummary?: string }) => {
      const headers: Record<string, string> = { "Content-Type": "text/plain" };
      if (changeSummary) headers["X-Change-Summary"] = changeSummary;
      return apiFetch(`/assets/${id}/content`, {
        method: "PUT",
        headers,
        body: content,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "asset-content"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "assets"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset-versions"] });
    },
  });
}

export function useAdminAssetVersions(assetId: string | null) {
  return useQuery({
    queryKey: ["admin", "asset-versions", assetId],
    queryFn: () =>
      apiFetch<PaginatedResponse<AssetVersion>>(
        `/assets/${assetId}/versions`,
      ),
    enabled: !!assetId,
  });
}

export function useAdminVersionContent(assetId: string | null, version: number) {
  return useQuery({
    queryKey: ["admin", "version-content", assetId, version],
    queryFn: async () => {
      const res = await apiFetchRaw(
        `/assets/${assetId}/versions/${version}/content`,
      );
      if (!res.ok) throw new Error("Failed to fetch version content");
      return res.text();
    },
    enabled: !!assetId && version > 0,
  });
}

export function useAdminRevertVersion() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ assetId, version }: { assetId: string; version: number }) =>
      apiFetch(`/assets/${assetId}/versions/${version}/revert`, {
        method: "POST",
      }),
    onSuccess: (_data, { assetId }) => {
      queryClient.invalidateQueries({ queryKey: ["admin", "asset", assetId] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset-content", assetId] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset-versions", assetId] });
      queryClient.invalidateQueries({ queryKey: ["admin", "assets"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Connection Instances (DB-managed)
// ---------------------------------------------------------------------------

export function useConnectionInstances() {
  return useQuery({
    queryKey: ["connection-instances"],
    queryFn: () => apiFetch<ConnectionInstance[]>("/connection-instances"),
  });
}

export function useConnectionInstance(kind: string, name: string) {
  return useQuery({
    queryKey: ["connection-instances", kind, name],
    queryFn: () => apiFetch<ConnectionInstance>(`/connection-instances/${kind}/${name}`),
    enabled: !!kind && !!name,
  });
}

export function useSetConnectionInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, name, ...body }: { kind: string; name: string; config: Record<string, any>; description?: string }) =>
      apiFetch<ConnectionInstance>(`/connection-instances/${kind}/${name}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["connection-instances"] });
      void qc.invalidateQueries({ queryKey: ["connections"] });
    },
  });
}

export function useDeleteConnectionInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, name }: { kind: string; name: string }) =>
      apiFetchRaw(`/connection-instances/${kind}/${name}`, { method: "DELETE" }).then((res) => {
        if (!res.ok) throw new Error("Failed to delete");
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["connection-instances"] });
      void qc.invalidateQueries({ queryKey: ["connections"] });
    },
  });
}

// ---------------------------------------------------------------------------
// API Gateway Catalogs (global OpenAPI spec bundles)
// ---------------------------------------------------------------------------

export interface APICatalogSummary {
  id: string;
  name: string;
  version?: string;
  display_name: string;
  description?: string;
  created_by?: string;
  created_at?: string;
  updated_at?: string;
  spec_count: number;
  ref_count: number;
}

export interface APICatalogSpec {
  spec_name: string;
  content?: string;
  source_kind: "inline" | "upload" | "url";
  source_url?: string;
  etag?: string;
  // Operator-set per-spec URL prefix applied at api_list_endpoints
  // and api_invoke_endpoint time. Empty means "use whatever the
  // spec's servers[0].url declares"; explicit non-empty overrides
  // the derivation. See pkg/toolkits/apigateway/catalog
  // NormalizeBasePath for the validation rules.
  base_path?: string;
  // Operator-set per-spec summary overrides surfaced by api_list_specs
  // and the multi-spec gate on api_list_endpoints. Empty means "derive
  // from the spec's info.title / info.description". See catalog
  // NormalizeSpecTitle / NormalizeSpecDescription for the rules
  // (trimmed, no CR/LF/NUL, capped at 200 / 2000 chars).
  title?: string;
  description?: string;
  last_fetched_at?: string;
  created_at?: string;
  updated_at?: string;
  // Number of operations the spec content parses to (one of the
  // GET/POST/PUT/DELETE/PATCH/HEAD pairs in every path item).
  operation_count?: number;
  // Number of persisted embedding rows. Equal to operation_count
  // when fully indexed; less while a job is in flight or has
  // failed.
  embedding_count?: number;
  // Most recent embedding job's state (pending|running|
  // succeeded|failed). Empty when no job has run yet for this
  // spec.
  embedding_status?: string;
  // Attempt counter from the most recent job, surfaced as
  // "running (attempt N)" in the badge.
  embedding_attempts?: number;
  // Most recent job's last_error column. Non-empty only when
  // the job is on a retry or has failed terminally.
  embedding_last_error?: string;
}

// EmbeddingHealth is the catalog-level roll-up rendered at the
// top of the catalog editor. Operators check this before
// considering a catalog production-ready ("all specs indexed"
// or "3 pending, 1 failed").
export interface APICatalogEmbeddingHealth {
  catalog_id: string;
  specs_total: number;
  specs_indexed: number;
  specs_pending: number;
  specs_running: number;
  specs_failed: number;
}

// EmbeddingJob is one row from api_catalog_embedding_jobs.
// Exposed by the admin embedding-jobs endpoint so the portal
// can show per-spec history.
export interface APICatalogEmbeddingJob {
  id: number;
  catalog_id: string;
  spec_name: string;
  kind: string;
  status: string;
  attempts: number;
  last_error?: string;
  worker_id?: string;
  next_run_at?: string;
  lease_expires_at?: string;
  created_at?: string;
  started_at?: string;
  completed_at?: string;
}

export function useAPICatalogs() {
  return useQuery({
    queryKey: ["api-catalogs"],
    queryFn: () => apiFetch<APICatalogSummary[]>("/api-catalogs"),
  });
}

export function useAPICatalog(id: string) {
  return useQuery({
    queryKey: ["api-catalogs", id],
    queryFn: () => apiFetch<APICatalogSummary>(`/api-catalogs/${id}`),
    enabled: !!id,
  });
}

export function useAPICatalogSpec(id: string, specName: string, enabled = true) {
  return useQuery({
    queryKey: ["api-catalogs", id, "specs", specName],
    queryFn: () =>
      apiFetch<APICatalogSpec>(`/api-catalogs/${id}/specs/${specName}`),
    enabled: enabled && !!id && !!specName,
  });
}

export function useCreateAPICatalog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      id: string;
      name: string;
      version?: string;
      display_name: string;
      description?: string;
    }) =>
      apiFetch<APICatalogSummary>("/api-catalogs", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}

export function useUpdateAPICatalog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: string;
      name?: string;
      version?: string;
      display_name?: string;
      description?: string;
    }) =>
      apiFetch<APICatalogSummary>(`/api-catalogs/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}

export function useDeleteAPICatalog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetchRaw(`/api-catalogs/${id}`, { method: "DELETE" }).then(async (res) => {
        if (!res.ok) {
          const body = await res.text();
          throw new Error(body || `delete failed: ${res.status}`);
        }
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}

export function useCloneAPICatalog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      sourceID,
      ...body
    }: {
      sourceID: string;
      id: string;
      name?: string;
      version?: string;
      display_name?: string;
    }) =>
      apiFetch<APICatalogSummary>(`/api-catalogs/${sourceID}/clone`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}

export function useUpsertAPICatalogSpec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      catalogID,
      specName,
      ...body
    }: {
      catalogID: string;
      specName: string;
      source_kind: "inline" | "url";
      content?: string;
      source_url?: string;
      base_path?: string;
      title?: string;
      description?: string;
    }) =>
      apiFetch<APICatalogSpec>(
        `/api-catalogs/${catalogID}/specs/${specName}`,
        { method: "PUT", body: JSON.stringify(body) },
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "specs", vars.specName],
      });
    },
  });
}

export function useUploadAPICatalogSpec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      catalogID,
      specName,
      file,
      base_path,
      title,
      description,
    }: {
      catalogID: string;
      specName: string;
      file: File;
      base_path?: string;
      title?: string;
      description?: string;
    }) => {
      const form = new FormData();
      form.append("file", file);
      const params = new URLSearchParams();
      if (base_path && base_path.trim() !== "") params.set("base_path", base_path.trim());
      if (title && title.trim() !== "") params.set("title", title.trim());
      if (description && description.trim() !== "") params.set("description", description.trim());
      const qs = params.toString() ? `?${params.toString()}` : "";
      const res = await apiFetchRaw(
        `/api-catalogs/${catalogID}/specs/${specName}/upload${qs}`,
        { method: "PUT", body: form },
      );
      if (!res.ok) {
        const body = await res.text();
        throw new Error(body || `upload failed: ${res.status}`);
      }
      return (await res.json()) as APICatalogSpec;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "specs", vars.specName],
      });
    },
  });
}

export function useRefreshAPICatalogSpec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ catalogID, specName }: { catalogID: string; specName: string }) =>
      apiFetch<APICatalogSpec>(
        `/api-catalogs/${catalogID}/specs/${specName}/refresh`,
        { method: "POST" },
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "specs", vars.specName],
      });
    },
  });
}

// useManualRetryEmbedding enqueues a manual_retry embedding job
// for the named spec. The button is an escape hatch (used only
// when an operator knows the dedup predicate's "same text,
// same model" check is wrong: model swapped externally, etc.).
// The automatic path (spec write enqueues a job; reconciler
// fills gaps) covers the common case without operator action.
//
// Returns 202 Accepted; the actual embedding happens off the
// request path. Caller polls the embedding health endpoint to
// see completion.
export function useManualRetryEmbedding() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ catalogID, specName }: { catalogID: string; specName: string }) =>
      apiFetch<{ status: string; created: boolean }>(
        `/api-catalogs/${catalogID}/specs/${specName}/reembed`,
        { method: "POST" },
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "specs", vars.specName],
      });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "embedding-health"],
      });
    },
  });
}

// useAPICatalogEmbeddingHealth polls the catalog-level
// embedding roll-up so the portal renders "all indexed" or
// "N pending, M failed" at the top of the catalog editor.
// Refetches every 5 seconds while the panel is mounted, since
// the worker runs off the request path and the operator needs
// the badge to reflect work as it completes.
export function useAPICatalogEmbeddingHealth(catalogID: string, enabled = true) {
  return useQuery({
    queryKey: ["api-catalogs", catalogID, "embedding-health"],
    queryFn: () =>
      apiFetch<APICatalogEmbeddingHealth>(
        `/api-catalogs/${catalogID}/embedding-health`,
      ),
    enabled: enabled && !!catalogID,
    refetchInterval: 5000,
  });
}

// EmbeddingProviderStatus mirrors the server-side embeddingProviderStatusResponse:
// the platform-wide embedding provider's kind, model, dimension, and a
// health enum. status="unconfigured" indicates the noop placeholder is
// in use and semantic features are disabled (the portal renders a
// banner on the Catalogs and Memory panels in this state). See #429.
export interface EmbeddingProviderStatus {
  kind: string;
  model: string;
  dimension: number;
  status: "ok" | "unconfigured";
}

// useEmbeddingProviderStatus polls the platform-wide embedding-provider
// status. Used by the Catalogs panel and the Memory settings panel to
// surface a banner when the provider is unconfigured.
export function useEmbeddingProviderStatus() {
  return useQuery({
    queryKey: ["admin", "embedding", "status"],
    queryFn: () => apiFetch<EmbeddingProviderStatus>("/embedding/status"),
    refetchInterval: 30000,
  });
}

// useAPICatalogEmbeddingStatuses returns one row per spec. The
// portal renders these as per-spec badges in the CatalogsPanel.
// Refetched on the same 5s cadence as the health roll-up so the
// two views stay coherent.
export function useAPICatalogEmbeddingStatuses(catalogID: string, enabled = true) {
  return useQuery({
    queryKey: ["api-catalogs", catalogID, "embedding-statuses"],
    queryFn: () =>
      apiFetch<{ specs: APICatalogEmbeddingSpecStatus[] }>(
        `/api-catalogs/${catalogID}/embedding-status`,
      ),
    enabled: enabled && !!catalogID,
    refetchInterval: 5000,
  });
}

// APICatalogEmbeddingSpecStatus mirrors the server-side
// embeddingStatusResponse: one row per spec with operation /
// embedding counts plus the most recent job's state.
export interface APICatalogEmbeddingSpecStatus {
  spec_name: string;
  operation_count: number;
  embedding_count: number;
  // embedded_so_far is the worker's in-flight chunk-progress counter.
  // While job_status is "running" the badge renders this against
  // operation_count so a long embed pass shows incremental progress
  // instead of staying at 0/N until the final atomic upsert commits
  // embedding_count in one tick. See #430.
  embedded_so_far?: number;
  job_status?: string;
  job_attempts?: number;
  job_last_error?: string;
  job_updated_at?: string;
}

export function useDeleteAPICatalogSpec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ catalogID, specName }: { catalogID: string; specName: string }) =>
      apiFetchRaw(`/api-catalogs/${catalogID}/specs/${specName}`, {
        method: "DELETE",
      }).then(async (res) => {
        if (!res.ok) {
          const body = await res.text();
          throw new Error(body || `delete failed: ${res.status}`);
        }
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Gateway-specific endpoints (test/refresh + enrichment rules)
// ---------------------------------------------------------------------------

export function useTestGatewayConnection() {
  return useMutation({
    mutationFn: ({ name, config }: { name: string; config: Record<string, any> }) =>
      apiFetch<import("./types").GatewayTestResponse>(
        `/gateway/connections/${name}/test`,
        { method: "POST", body: JSON.stringify({ config }) },
      ),
  });
}

export function useRefreshGatewayConnection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch<import("./types").GatewayRefreshResponse>(
        `/gateway/connections/${name}/refresh`,
        { method: "POST" },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["connections"] });
      void qc.invalidateQueries({ queryKey: ["tools"] });
    },
  });
}

export function useGatewayConnectionStatus(name: string, enabled = true) {
  return useQuery({
    queryKey: ["gateway-status", name],
    queryFn: () =>
      apiFetch<import("./types").GatewayConnectionStatus>(
        `/gateway/connections/${name}/status`,
      ),
    enabled: enabled && !!name,
    refetchInterval: REFETCH_INTERVAL,
  });
}

export function useReacquireGatewayOAuth() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch<import("./types").GatewayConnectionStatus>(
        `/gateway/connections/${name}/reacquire-oauth`,
        { method: "POST" },
      ),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: ["gateway-status", name] });
    },
  });
}

// useStartConnectionOAuth kicks off the authorization_code flow for
// the given kind ("mcp", "api", future kinds). The backend dispatches
// on the kind path parameter to the corresponding OAuthKindHandler.
// Replaces the prior per-kind useStartGatewayOAuth /
// useStartAPIGatewayOAuth hooks that targeted divergent routes against
// divergent token stores — every kind now shares one
// /connections/{kind}/{name}/oauth-start route backed by the unified
// connoauth.Store.
export function useStartConnectionOAuth(kind: string) {
  return useMutation({
    mutationFn: ({ name, returnURL }: { name: string; returnURL?: string }) =>
      apiFetch<import("./types").GatewayOAuthStartResponse>(
        `/connections/${kind}/${name}/oauth-start`,
        {
          method: "POST",
          body: JSON.stringify({ return_url: returnURL ?? "" }),
        },
      ),
  });
}

// useStartGatewayOAuth is the MCP-kind specialization of
// useStartConnectionOAuth. Kept as a named export so the existing MCP
// gateway form callsite is unchanged through this refactor.
export function useStartGatewayOAuth() {
  return useStartConnectionOAuth("mcp");
}

// useStartAPIGatewayOAuth is the API-kind specialization of
// useStartConnectionOAuth. Kept as a named export so the existing
// HTTP API gateway form callsite is unchanged through this refactor.
export function useStartAPIGatewayOAuth() {
  return useStartConnectionOAuth("api");
}

// useConnectionsOAuthHealth polls the bulk OAuth-health endpoint on
// a 10-second cadence. Returns one row per connection with the bits
// the connection-list view needs to render a per-row badge without
// fanning out N per-row /oauth-status calls. Polling interval
// matches useConnectionOAuthStatus so a long-stale failure surface
// is bounded the same way the per-connection card is.
export function useConnectionsOAuthHealth() {
  return useQuery({
    queryKey: ["connections-oauth-health"],
    queryFn: () =>
      apiFetch<import("./types").ConnectionsOAuthHealthResponse>(
        `/connections/oauth-health`,
      ),
    refetchInterval: 10000,
  });
}

// useConnectionOAuthStatus returns the unified OAuth status snapshot
// for ANY connection kind. Renders the status card in both the MCP
// gateway and HTTP API gateway connection views.
export function useConnectionOAuthStatus(kind: string, name: string, enabled = true) {
  return useQuery({
    queryKey: ["connection-oauth-status", kind, name],
    queryFn: () =>
      apiFetch<import("./types").ConnectionOAuthStatus>(
        `/connections/${kind}/${name}/oauth-status`,
      ),
    enabled: enabled && !!kind && !!name,
    refetchInterval: 10000,
  });
}

// useConnectionAuthEvents returns the most recent 30 OAuth-lifecycle
// events for the given connection. Powers the History panel under the
// OAuth status card so operators can see why a token vanished without
// reading pod logs.
export function useConnectionAuthEvents(kind: string, name: string, enabled = true) {
  return useQuery({
    queryKey: ["connection-auth-events", kind, name],
    queryFn: () =>
      apiFetch<import("./types").ConnectionAuthEvent[]>(
        `/connections/${kind}/${name}/auth-events`,
      ),
    enabled: enabled && !!kind && !!name,
    refetchInterval: 30000,
  });
}

// useReacquireConnectionOAuth forces a refresh-token exchange for ANY
// connection kind. Useful from the admin status card to verify the
// persisted refresh token still works against the IdP.
export function useReacquireConnectionOAuth() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, name }: { kind: string; name: string }) =>
      apiFetch<void>(`/connections/${kind}/${name}/reacquire-oauth`, {
        method: "POST",
      }),
    onSuccess: (_, { kind, name }) => {
      void qc.invalidateQueries({
        queryKey: ["connection-oauth-status", kind, name],
      });
    },
  });
}

export function useEnrichmentRules(connection: string, enabled = true) {
  return useQuery({
    queryKey: ["enrichment-rules", connection],
    queryFn: () =>
      apiFetch<import("./types").EnrichmentRule[]>(
        `/gateway/connections/${connection}/enrichment-rules`,
      ),
    enabled: enabled && !!connection,
  });
}

export function useCreateEnrichmentRule(connection: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: import("./types").EnrichmentRuleBody) =>
      apiFetch<import("./types").EnrichmentRule>(
        `/gateway/connections/${connection}/enrichment-rules`,
        { method: "POST", body: JSON.stringify(body) },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["enrichment-rules", connection] });
    },
  });
}

export function useUpdateEnrichmentRule(connection: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & import("./types").EnrichmentRuleBody) =>
      apiFetch<import("./types").EnrichmentRule>(
        `/gateway/connections/${connection}/enrichment-rules/${id}`,
        { method: "PUT", body: JSON.stringify(body) },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["enrichment-rules", connection] });
    },
  });
}

export function useDeleteEnrichmentRule(connection: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetchRaw(`/gateway/connections/${connection}/enrichment-rules/${id}`, {
        method: "DELETE",
      }).then((res) => {
        if (!res.ok) throw new Error("Failed to delete enrichment rule");
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["enrichment-rules", connection] });
    },
  });
}

export function useDryRunEnrichmentRule(connection: string) {
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: import("./types").DryRunRequest }) =>
      apiFetch<import("./types").DryRunResponse>(
        `/gateway/connections/${connection}/enrichment-rules/${id}/dry-run`,
        { method: "POST", body: JSON.stringify(body) },
      ),
  });
}

// ---------------------------------------------------------------------------
// API Keys
// ---------------------------------------------------------------------------

export function useAPIKeys() {
  return useQuery({
    queryKey: ["auth", "keys"],
    queryFn: () => apiFetch<import("./types").APIKeyListResponse>("/auth/keys"),
  });
}

export function useCreateAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; email?: string; description?: string; roles: string[]; expires_in?: string }) =>
      apiFetch<import("./types").APIKeyCreateResponse>("/auth/keys", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["auth", "keys"] });
    },
  });
}

export function useDeleteAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetchRaw(`/auth/keys/${name}`, { method: "DELETE" }).then((res) => {
        if (!res.ok) throw new Error("Failed to delete");
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["auth", "keys"] });
    },
  });
}

// --- Config entries ---

export function useConfigEntries() {
  return useQuery({
    queryKey: ["config", "entries"],
    queryFn: () => apiFetch<import("./types").ConfigEntry[]>("/config/entries"),
  });
}

export function useConfigEntry(key: string) {
  return useQuery({
    queryKey: ["config", "entries", key],
    queryFn: () => apiFetch<import("./types").ConfigEntry>(`/config/entries/${key}`),
    enabled: !!key,
    retry: false,
  });
}

export function useSetConfigEntry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key, value }: { key: string; value: string }) =>
      apiFetch<import("./types").ConfigEntry>(`/config/entries/${key}`, {
        method: "PUT",
        body: JSON.stringify({ value }),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["config", "entries"] });
      void qc.invalidateQueries({ queryKey: ["config", "effective"] });
      void qc.invalidateQueries({ queryKey: ["system", "info"] });
    },
  });
}

export function useDeleteConfigEntry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (key: string) =>
      apiFetchRaw(`/config/entries/${key}`, { method: "DELETE" }).then((res) => {
        if (!res.ok) throw new Error("Failed to delete");
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["config", "entries"] });
      void qc.invalidateQueries({ queryKey: ["config", "effective"] });
      void qc.invalidateQueries({ queryKey: ["system", "info"] });
    },
  });
}

export function useConfigChangelog() {
  return useQuery({
    queryKey: ["config", "changelog"],
    queryFn: () => apiFetch<import("./types").ConfigChangelogEntry[]>("/config/changelog"),
  });
}

export function useEffectiveConfig() {
  return useQuery({
    queryKey: ["config", "effective"],
    queryFn: () => apiFetch<import("./types").EffectiveConfigEntry[]>("/config/effective"),
  });
}

export function useEffectiveConnections() {
  return useQuery({
    queryKey: ["connection-instances", "effective"],
    queryFn: () => apiFetch<import("./types").EffectiveConnection[]>("/connection-instances/effective"),
  });
}

// ---------------------------------------------------------------------------
// Prompts
// ---------------------------------------------------------------------------

interface AdminPromptsParams {
  search?: string;
  scope?: string;
  owner_email?: string;
}

export function useAdminPrompts(params: AdminPromptsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.search) searchParams.set("search", params.search);
  if (params.scope) searchParams.set("scope", params.scope);
  if (params.owner_email) searchParams.set("owner_email", params.owner_email);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["admin", "prompts", params],
    queryFn: () => apiFetch<PromptListResponse>(`/prompts${qs ? `?${qs}` : ""}`),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}

export function useAdminPrompt(id: string | null) {
  return useQuery({
    queryKey: ["admin", "prompt", id],
    queryFn: () => apiFetch<Prompt>(`/prompts/${id}`),
    enabled: !!id,
  });
}

export function useCreateAdminPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<Prompt>) =>
      apiFetch<Prompt>("/prompts", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "prompts"] });
    },
  });
}

export function useUpdateAdminPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & Partial<Prompt>) =>
      apiFetch<Prompt>(`/prompts/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "prompts"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "prompt"] });
    },
  });
}

export function useDeleteAdminPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/prompts/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "prompts"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Memory
// ---------------------------------------------------------------------------

interface MemoryRecordsParams {
  page?: number;
  perPage?: number;
  persona?: string;
  dimension?: string;
  category?: string;
  status?: string;
  source?: string;
  createdBy?: string;
  entityUrn?: string;
}

export function useMemoryRecords(params: MemoryRecordsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.perPage) searchParams.set("per_page", String(params.perPage));
  if (params.persona) searchParams.set("persona", params.persona);
  if (params.dimension) searchParams.set("dimension", params.dimension);
  if (params.category) searchParams.set("category", params.category);
  if (params.status) searchParams.set("status", params.status);
  if (params.source) searchParams.set("source", params.source);
  if (params.createdBy) searchParams.set("created_by", params.createdBy);
  if (params.entityUrn) searchParams.set("entity_urn", params.entityUrn);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["memory", "records", params],
    queryFn: () =>
      apiFetch<MemoryListResponse>(
        `/memory/records${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}

export function useMemoryStats() {
  return useQuery({
    queryKey: ["memory", "stats"],
    queryFn: () => apiFetch<MemoryStats>("/memory/records/stats"),
    refetchInterval: REFETCH_INTERVAL,
  });
}

export function useUpdateMemory() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: string;
      content?: string;
      category?: string;
      confidence?: string;
      dimension?: string;
      metadata?: Record<string, unknown>;
    }) =>
      apiFetch(`/memory/records/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["memory"] });
    },
  });
}

export function useArchiveMemory() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/memory/records/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["memory"] });
    },
  });
}
