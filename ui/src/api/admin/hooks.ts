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
  PersonaListResponse,
  PersonaDetail,
  PersonaCreateRequest,
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

export function useStartGatewayOAuth() {
  return useMutation({
    mutationFn: ({ name, returnURL }: { name: string; returnURL?: string }) =>
      apiFetch<import("./types").GatewayOAuthStartResponse>(
        `/gateway/connections/${name}/oauth-start`,
        {
          method: "POST",
          body: JSON.stringify({ return_url: returnURL ?? "" }),
        },
      ),
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
