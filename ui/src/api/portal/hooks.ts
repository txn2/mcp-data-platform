import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "./client";
import type { ResolvedRef } from "@/lib/entityRefs";
import type {
  Asset,
  AssetVersion,
  AssetResponse,
  Share,
  SharedAsset,
  PaginatedResponse,
  ShareResponse,
  SharePermission,
  ActivityOverview,
  TimeseriesBucket,
  BreakdownEntry,
  Insight,
  InsightStats,
  MemoryRecord,
  MemoryStats,
  ScoredMemoryRecord,
  ScoredInsight,
  ScoredAsset,
  ScoredCollection,
  Collection,
  CollectionConfig,
  CollectionResponse,
  SharedCollection,
  Thread,
  ThreadWithMeta,
  ThreadActivityItem,
  ThreadEvent,
  ThreadKind,
  ThreadStatus,
  ThreadTargetType,
  ThreadEventType,
  ThreadAnchor,
  ThreadChain,
  ThreadCounts,
  SignoffSummary,
  KnowledgePage,
  KnowledgePageInput,
  KnowledgePageListResponse,
  KnowledgePageVersionsResponse,
  ScoredKnowledgePage,
  SearchResponse,
} from "./types";

// --- Branding (unauthenticated) ---

export interface Branding {
  name: string;
  version: string;
  portal_title: string;
  portal_tagline?: string;
  oidc_button_label?: string;
  portal_logo: string;
  portal_logo_light: string;
  portal_logo_dark: string;
  oidc_enabled: boolean;
}

export function useBranding() {
  return useQuery({
    queryKey: ["branding"],
    queryFn: async () => {
      const res = await fetch("/api/v1/admin/public/branding");
      if (!res.ok) return null;
      return res.json() as Promise<Branding>;
    },
    staleTime: 5 * 60_000, // cache for 5 minutes
    retry: false,
  });
}

// --- Queries ---

export function useAssets(params?: {
  content_type?: string;
  tag?: string;
  limit?: number;
  offset?: number;
}) {
  const searchParams = new URLSearchParams();
  if (params?.content_type) searchParams.set("content_type", params.content_type);
  if (params?.tag) searchParams.set("tag", params.tag);
  if (params?.limit) searchParams.set("limit", String(params.limit));
  if (params?.offset) searchParams.set("offset", String(params.offset));
  const qs = searchParams.toString();

  return useQuery({
    queryKey: ["assets", params],
    queryFn: () =>
      apiFetch<PaginatedResponse<Asset>>(`/assets${qs ? `?${qs}` : ""}`),
  });
}

// useSearchAssets ranks the caller's own assets by relevance to a free-text
// query (semantic + keyword, server-side). Disabled when the query is empty so
// the asset library falls back to the plain list. Mirrors useSearchMyMemories.
export function useSearchAssets(query: string, params?: { limit?: number }) {
  const q = query.trim();
  const sp = new URLSearchParams({ q });
  if (params?.limit) sp.set("limit", String(params.limit));

  return useQuery({
    queryKey: ["search-assets", q, params],
    enabled: q.length > 0,
    queryFn: () =>
      apiFetch<PaginatedResponse<ScoredAsset>>(`/assets/search?${sp.toString()}`),
  });
}

export function useAsset(id: string) {
  return useQuery({
    queryKey: ["asset", id],
    queryFn: () => apiFetch<AssetResponse>(`/assets/${id}`),
    enabled: !!id,
  });
}

/**
 * Maximum size in bytes before the viewer skips auto-loading content.
 * Assets larger than this show a "too large to preview" message with a download button.
 * The content can still be fetched explicitly by the user.
 */
export const LARGE_ASSET_THRESHOLD = 2 * 1024 * 1024; // 2 MB

export function useAssetContent(id: string, sizeBytes?: number) {
  const tooLarge = sizeBytes != null && sizeBytes > LARGE_ASSET_THRESHOLD;
  return useQuery({
    queryKey: ["asset-content", id],
    queryFn: async () => {
      const res = await apiFetchRaw(`/assets/${id}/content`);
      if (!res.ok) throw new Error("Failed to fetch content");
      return res.text();
    },
    enabled: !!id && !tooLarge,
  });
}

export function useShares(assetId: string) {
  return useQuery({
    queryKey: ["shares", assetId],
    queryFn: () => apiFetch<Share[]>(`/assets/${assetId}/shares`),
    enabled: !!assetId,
  });
}

export function useSharedWithMe(params?: { limit?: number; offset?: number }) {
  const searchParams = new URLSearchParams();
  if (params?.limit) searchParams.set("limit", String(params.limit));
  if (params?.offset) searchParams.set("offset", String(params.offset));
  const qs = searchParams.toString();

  return useQuery({
    queryKey: ["shared-with-me", params],
    queryFn: () =>
      apiFetch<PaginatedResponse<SharedAsset>>(
        `/shared-with-me${qs ? `?${qs}` : ""}`,
      ),
  });
}

// --- Mutations ---

export function useUpdateAsset() {
  const qc = useQueryClient();
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
      void qc.invalidateQueries({ queryKey: ["assets"] });
      void qc.invalidateQueries({ queryKey: ["asset"] });
    },
  });
}

export function useDeleteAsset() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/assets/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["assets"] });
    },
  });
}

export function useUpdateAssetContent() {
  const qc = useQueryClient();
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
      void qc.invalidateQueries({ queryKey: ["asset-content"] });
      void qc.invalidateQueries({ queryKey: ["asset"] });
      void qc.invalidateQueries({ queryKey: ["assets"] });
      void qc.invalidateQueries({ queryKey: ["asset-versions"] });
    },
  });
}

export function useUploadThumbnail() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, blob }: { id: string; blob: Blob }) => {
      const res = await apiFetchRaw(`/assets/${id}/thumbnail`, {
        method: "PUT",
        headers: { "Content-Type": "image/png" },
        body: blob,
      });
      if (!res.ok) throw new Error("Failed to upload thumbnail");
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["assets"] });
      void qc.invalidateQueries({ queryKey: ["asset"] });
    },
  });
}

export function useCreateShare() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      assetId,
      ...body
    }: {
      assetId: string;
      expires_in?: string;
      shared_with_user_id?: string;
      shared_with_email?: string;
      hide_expiration?: boolean;
      notice_text?: string;
      permission?: SharePermission;
    }) =>
      apiFetch<ShareResponse>(`/assets/${assetId}/shares`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["shares"] });
    },
  });
}

export function useRevokeShare() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (shareId: string) =>
      apiFetch(`/shares/${shareId}`, { method: "DELETE" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["shares"] });
      void qc.invalidateQueries({ queryKey: ["collection-shares"] });
      void qc.invalidateQueries({ queryKey: ["prompt-shares"] });
    },
  });
}

export function useCopyAsset() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<Asset>(`/assets/${id}/copy`, { method: "POST" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["assets"] });
    },
  });
}

export function useCreateAsset() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      name: string;
      description?: string;
      content_type: string;
      content: string;
      tags?: string[];
    }) =>
      apiFetch<Asset>("/assets", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["assets"] });
    },
  });
}

// --- Versions ---

export function useAssetVersions(assetId: string) {
  return useQuery({
    queryKey: ["asset-versions", assetId],
    queryFn: () =>
      apiFetch<PaginatedResponse<AssetVersion>>(
        `/assets/${assetId}/versions`,
      ),
    enabled: !!assetId,
  });
}

export function useVersionContent(assetId: string, version: number) {
  return useQuery({
    queryKey: ["version-content", assetId, version],
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

export function useRevertVersion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ assetId, version }: { assetId: string; version: number }) =>
      apiFetch(`/assets/${assetId}/versions/${version}/revert`, {
        method: "POST",
      }),
    onSuccess: (_data, { assetId }) => {
      void qc.invalidateQueries({ queryKey: ["asset", assetId] });
      void qc.invalidateQueries({ queryKey: ["asset-content", assetId] });
      void qc.invalidateQueries({ queryKey: ["asset-versions", assetId] });
      void qc.invalidateQueries({ queryKey: ["assets"] });
    },
  });
}

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

// --- Collections ---

export function useCollections(params?: { search?: string; limit?: number; offset?: number }) {
  const searchParams = new URLSearchParams();
  if (params?.search) searchParams.set("search", params.search);
  if (params?.limit) searchParams.set("limit", String(params.limit));
  if (params?.offset) searchParams.set("offset", String(params.offset));
  const qs = searchParams.toString();

  return useQuery({
    queryKey: ["collections", params],
    queryFn: () =>
      apiFetch<PaginatedResponse<Collection>>(`/collections${qs ? `?${qs}` : ""}`),
  });
}

// useSearchCollections ranks the caller's own collections by relevance to a
// free-text query (matching name, description, and section text; semantic +
// keyword, server-side). Disabled when the query is empty.
export function useSearchCollections(query: string, params?: { limit?: number }) {
  const q = query.trim();
  const sp = new URLSearchParams({ q });
  if (params?.limit) sp.set("limit", String(params.limit));

  return useQuery({
    queryKey: ["search-collections", q, params],
    enabled: q.length > 0,
    queryFn: () =>
      apiFetch<PaginatedResponse<ScoredCollection>>(`/collections/search?${sp.toString()}`),
  });
}

export function useCollection(id: string) {
  return useQuery({
    queryKey: ["collection", id],
    queryFn: () => apiFetch<CollectionResponse>(`/collections/${id}`),
    enabled: !!id,
  });
}

export function useCreateCollection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; description?: string }) =>
      apiFetch<Collection>("/collections", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["collections"] });
    },
  });
}

export function useUpdateCollection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; name?: string; description?: string }) =>
      apiFetch<Collection>(`/collections/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["collections"] });
      void qc.invalidateQueries({ queryKey: ["collection"] });
    },
  });
}

export function useDeleteCollection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/collections/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["collections"] });
    },
  });
}

export function useUpdateCollectionSections() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, sections }: {
      id: string;
      sections: { title: string; description?: string; items: { asset_id: string }[] }[];
    }) =>
      apiFetch<CollectionResponse>(`/collections/${id}/sections`, {
        method: "PUT",
        body: JSON.stringify({ sections }),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["collection"] });
      void qc.invalidateQueries({ queryKey: ["collections"] });
    },
  });
}

export function useUpdateCollectionConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, config }: { id: string; config: CollectionConfig }) =>
      apiFetch<Collection>(`/collections/${id}/config`, {
        method: "PUT",
        body: JSON.stringify(config),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["collection"] });
      void qc.invalidateQueries({ queryKey: ["collections"] });
    },
  });
}

export function useUploadCollectionThumbnail() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, blob }: { id: string; blob: Blob }) => {
      const res = await apiFetchRaw(`/collections/${id}/thumbnail`, {
        method: "PUT",
        headers: { "Content-Type": "image/png" },
        body: blob,
      });
      if (!res.ok) throw new Error("Failed to upload thumbnail");
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["collections"] });
      void qc.invalidateQueries({ queryKey: ["collection"] });
    },
  });
}

export function useCollectionShares(collectionId: string) {
  return useQuery({
    queryKey: ["collection-shares", collectionId],
    queryFn: () => apiFetch<Share[]>(`/collections/${collectionId}/shares`),
    enabled: !!collectionId,
  });
}

export function useCreateCollectionShare() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      collectionId,
      ...body
    }: {
      collectionId: string;
      expires_in?: string;
      shared_with_user_id?: string;
      shared_with_email?: string;
      hide_expiration?: boolean;
      notice_text?: string;
      permission?: SharePermission;
    }) =>
      apiFetch<ShareResponse>(`/collections/${collectionId}/shares`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: (_, vars) => {
      void qc.invalidateQueries({ queryKey: ["collection-shares", vars.collectionId] });
      void qc.invalidateQueries({ queryKey: ["collections"] });
    },
  });
}

export function usePromptShares(promptId: string) {
  return useQuery({
    queryKey: ["prompt-shares", promptId],
    queryFn: () => apiFetch<Share[]>(`/prompts/${promptId}/shares`),
    enabled: !!promptId,
  });
}

export function useCreatePromptShare() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      promptId,
      ...body
    }: {
      promptId: string;
      shared_with_user_id?: string;
      shared_with_email?: string;
      permission?: SharePermission;
    }) =>
      apiFetch<ShareResponse>(`/prompts/${promptId}/shares`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: (_, vars) => {
      void qc.invalidateQueries({ queryKey: ["prompt-shares", vars.promptId] });
    },
  });
}

// SharedPromptItem is a prompt shared with the current user plus share metadata.
export interface SharedPromptItem {
  prompt: import("@/api/admin/types").Prompt;
  share_id: string;
  shared_by: string;
  shared_at: string;
  permission: SharePermission;
}

export function useSharedPrompts() {
  return useQuery({
    queryKey: ["shared-prompts"],
    queryFn: () => apiFetch<SharedPromptItem[]>("/shared-prompts"),
  });
}

export function useSharedCollections(params?: { limit?: number; offset?: number }) {
  const searchParams = new URLSearchParams();
  if (params?.limit) searchParams.set("limit", String(params.limit));
  if (params?.offset) searchParams.set("offset", String(params.offset));
  const qs = searchParams.toString();

  return useQuery({
    queryKey: ["shared-collections", params],
    queryFn: () =>
      apiFetch<PaginatedResponse<SharedCollection>>(
        `/shared-collections${qs ? `?${qs}` : ""}`,
      ),
  });
}

// ---------------------------------------------------------------------------
// Prompts
// ---------------------------------------------------------------------------

interface PortalPromptListResponse {
  personal: import("@/api/admin/types").Prompt[];
  available: import("@/api/admin/types").Prompt[];
}

export function useMyPrompts() {
  return useQuery({
    queryKey: ["portal", "prompts"],
    queryFn: () => apiFetch<PortalPromptListResponse>("/prompts"),
  });
}

// ScoredPrompt pairs a prompt with its relevance score, as returned by the
// ranked prompt search endpoint.
export interface ScoredPrompt {
  prompt: import("@/api/admin/types").Prompt;
  score: number;
}

// useSearchMyPrompts ranks approved prompts visible to the caller by relevance
// to query. Disabled (no request) until query is non-empty, so the list
// endpoint remains the default browse experience.
export function useSearchMyPrompts(query: string, params?: { limit?: number }) {
  const q = query.trim();
  const sp = new URLSearchParams({ q });
  if (params?.limit) sp.set("limit", String(params.limit));

  return useQuery({
    queryKey: ["search-my-prompts", q, params],
    enabled: q.length > 0,
    queryFn: () =>
      apiFetch<PaginatedResponse<ScoredPrompt>>(
        `/prompts/search?${sp.toString()}`,
      ),
  });
}

export function useCreateMyPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      name: string;
      display_name?: string;
      description?: string;
      content: string;
      arguments?: { name: string; description: string; required: boolean }[];
      category?: string;
      tags?: string[];
    }) =>
      apiFetch<import("@/api/admin/types").Prompt>("/prompts", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
  });
}

export function useUpdateMyPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: {
      id: string;
      name?: string;
      display_name?: string;
      description?: string;
      content?: string;
      category?: string;
      tags?: string[];
      arguments?: { name: string; description: string; required: boolean }[];
      requested_scope?: string;
      requested_personas?: string[];
    }) =>
      apiFetch<import("@/api/admin/types").Prompt>(`/prompts/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
  });
}

export function useDeleteMyPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/prompts/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
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

// --- Unified knowledge search (#661) ---

// useSearch fans one query across every source the caller can access (internal
// knowledge pages, the DataHub catalog, memory, insights, assets, prompts,
// endpoints, connections), returning results grouped by source with a coverage
// summary. It is the REST surface over the same router behind the MCP search
// tool. Disabled (no request) until query or entityUrns is non-empty, so an
// empty query falls back to the page's browse experience.
// MIN_SEARCH_LEN is the shortest free-text query that issues a server search.
// Single-character full-text queries are wasteful and return noise, so search
// surfaces wait for at least this many characters (debounced) before querying.
export const MIN_SEARCH_LEN = 2;

export function useSearch(
  query: string,
  params?: { entityUrns?: string[]; sources?: string[]; status?: string; limit?: number },
) {
  const q = query.trim();
  const sp = new URLSearchParams();
  if (q) sp.set("q", q);
  for (const urn of params?.entityUrns ?? []) sp.append("entity_urns", urn);
  for (const src of params?.sources ?? []) sp.append("sources", src);
  if (params?.status) sp.set("status", params.status);
  if (params?.limit) sp.set("limit", String(params.limit));

  const hasEntityURNs = (params?.entityUrns?.length ?? 0) > 0;
  return useQuery({
    queryKey: ["unified-search", q, params],
    // Free-text searches wait for the minimum query length; an entity-URN lookup
    // is exact, so it is exempt.
    enabled: q.length >= MIN_SEARCH_LEN || hasEntityURNs,
    queryFn: () => apiFetch<SearchResponse>(`/search?${sp.toString()}`),
  });
}

// --- Feedback threads (#601) ---

export interface ThreadListFilter {
  target_type?: ThreadTargetType;
  asset_id?: string;
  collection_id?: string;
  prompt_id?: string;
  knowledge_page_id?: string;
  kind?: ThreadKind;
  status?: ThreadStatus;
  limit?: number;
  offset?: number;
}

function threadQuery(filter: ThreadListFilter): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(filter)) {
    if (v !== undefined && v !== null && v !== "") sp.set(k, String(v));
  }
  const qs = sp.toString();
  return qs ? `?${qs}` : "";
}

// A list filter is "scoped" once it targets a single object or the standalone
// channel; until then the query is disabled (the backend requires a scope).
function threadFilterScoped(filter: ThreadListFilter): boolean {
  return (
    filter.target_type === "standalone" ||
    !!filter.asset_id ||
    !!filter.collection_id ||
    !!filter.prompt_id ||
    !!filter.knowledge_page_id
  );
}

export function useThreads(filter: ThreadListFilter) {
  return useQuery({
    queryKey: ["threads", filter],
    queryFn: () =>
      apiFetch<PaginatedResponse<ThreadWithMeta>>(`/threads${threadQuery(filter)}`),
    enabled: threadFilterScoped(filter),
  });
}

export function useThread(id: string) {
  return useQuery({
    queryKey: ["thread", id],
    queryFn: () => apiFetch<Thread>(`/threads/${id}`),
    enabled: !!id,
  });
}

export function useThreadEvents(id: string) {
  return useQuery({
    queryKey: ["thread-events", id],
    queryFn: () =>
      apiFetch<{ data: ThreadEvent[] }>(`/threads/${id}/events`).then((r) => r.data),
    enabled: !!id,
  });
}

// Worklists / inbox (#603). Practitioner = open resolution-required threads on
// my artifacts; SME = threads awaiting my validation.
export function usePractitionerWorklist(enabled = true) {
  return useQuery({
    queryKey: ["worklist", "practitioner"],
    queryFn: () => apiFetch<PaginatedResponse<ThreadWithMeta>>(`/worklist/practitioner`),
    enabled,
  });
}

export function useSMEWorklist(enabled = true) {
  return useQuery({
    queryKey: ["worklist", "sme"],
    queryFn: () => apiFetch<PaginatedResponse<ThreadWithMeta>>(`/worklist/sme`),
    enabled,
  });
}

// useFeedbackActivity fetches the unified feed (#617): every feedback thread on
// an asset, collection, or prompt the caller can view, most recent first. With
// no push notifications, this is how a user discovers new feedback on their work.
export function useFeedbackActivity(enabled = true) {
  return useQuery({
    queryKey: ["feedback", "activity"],
    queryFn: () => apiFetch<PaginatedResponse<ThreadActivityItem>>(`/feedback/activity`),
    enabled,
  });
}

// useSignoff fetches "signed off by N of M" for an asset or collection (#603).
export function useSignoff(targetType: "assets" | "collections", id: string, enabled = true) {
  return useQuery({
    queryKey: ["signoff", targetType, id],
    queryFn: () => apiFetch<SignoffSummary>(`/${targetType}/${id}/signoff`),
    enabled: !!id && enabled,
  });
}

// useRespondValidation lets the feedback author mark a thread validated/disputed
// (#603). Disputing re-opens the thread server-side.
export function useRespondValidation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      threadId,
      result,
      reason,
    }: {
      threadId: string;
      result: "validated" | "disputed";
      reason?: string;
    }) =>
      apiFetch<Thread>(`/threads/${threadId}/validation`, {
        method: "POST",
        body: JSON.stringify({ result, reason }),
      }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}

// useThreadChain fetches the resolved thread -> insight -> changeset chain
// (#602). Only enabled once a thread has been linked to an insight; an
// unlinked thread has nothing to show and we avoid the round-trip.
export function useThreadChain(id: string, hasInsight: boolean) {
  return useQuery({
    queryKey: ["thread-chain", id],
    queryFn: () => apiFetch<ThreadChain>(`/threads/${id}/chain`),
    enabled: !!id && hasInsight,
  });
}

export function useThreadCounts(
  targetType: "asset" | "collection" | "knowledge_page",
  ids: string[],
) {
  const sorted = [...ids].sort();
  return useQuery({
    queryKey: ["thread-counts", targetType, sorted],
    queryFn: () => {
      const sp = new URLSearchParams({ target_type: targetType, ids: sorted.join(",") });
      return apiFetch<ThreadCounts>(`/threads/counts?${sp.toString()}`);
    },
    enabled: sorted.length > 0,
  });
}

export interface CreateThreadInput {
  kind: ThreadKind;
  target_type: ThreadTargetType;
  asset_id?: string;
  collection_id?: string;
  prompt_id?: string;
  knowledge_page_id?: string;
  anchor?: ThreadAnchor;
  target_version?: number;
  title?: string;
  requires_resolution?: boolean;
  body: string;
  rating?: number;
}

// invalidateThreadQueries refreshes every thread-related cache key after a
// mutation so lists, detail, timeline, and badges all reflect the change.
function invalidateThreadQueries(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: ["threads"] });
  void qc.invalidateQueries({ queryKey: ["thread"] });
  void qc.invalidateQueries({ queryKey: ["thread-events"] });
  void qc.invalidateQueries({ queryKey: ["thread-counts"] });
  // The activity feed and worklists (#617) are cross-artifact thread views, so a
  // create/reply/status change must refresh them too, not just the scoped lists.
  void qc.invalidateQueries({ queryKey: ["feedback"] });
  void qc.invalidateQueries({ queryKey: ["worklist"] });
}

export function useCreateThread() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateThreadInput) =>
      apiFetch<Thread>(`/threads`, {
        method: "POST",
        body: JSON.stringify(input),
      }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}

// Capturing a feedback thread as a reviewable insight (#662). The optional
// fields override the defaults (content derived from the thread, sink class
// business_knowledge). Requires apply_knowledge access server-side.
export interface CaptureThreadInsightInput {
  threadId: string;
  content?: string;
  category?: string;
  confidence?: string;
  sink_class?: string;
  entity_urns?: string[];
}

export interface CaptureThreadInsightResult {
  insight_id: string;
  status: string;
  linked: boolean;
}

export function useCaptureThreadInsight() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ threadId, ...body }: CaptureThreadInsightInput) =>
      apiFetch<CaptureThreadInsightResult>(`/threads/${threadId}/insight`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      invalidateThreadQueries(qc);
      // A new pending insight enters the review queue and its stats.
      void qc.invalidateQueries({ queryKey: ["insights"] });
      void qc.invalidateQueries({ queryKey: ["insight-stats"] });
    },
  });
}

export interface AppendThreadEventInput {
  threadId: string;
  event_type?: ThreadEventType;
  body?: string;
  rating?: number;
  parent_event_id?: string;
}

export function useAppendThreadEvent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ threadId, ...body }: AppendThreadEventInput) =>
      apiFetch<ThreadEvent>(`/threads/${threadId}/events`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}

export interface UpdateThreadInput {
  id: string;
  status?: ThreadStatus;
  requires_resolution?: boolean;
  // validation_state is intentionally not settable via the generic update:
  // validation transitions go through useRespondValidation (#603).
}

export function useUpdateThread() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: UpdateThreadInput) =>
      apiFetch<Thread>(`/threads/${id}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}

export function useDeleteThread() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<{ status: string }>(`/threads/${id}`, { method: "DELETE" }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}

// --- Known-users directory for the share picker (#614) ---

export function useDirectoryUsers(q: string, enabled = true) {
  const query = q ? `?q=${encodeURIComponent(q)}` : "";
  return useQuery({
    queryKey: ["portal", "directory-users", q],
    queryFn: () =>
      apiFetch<import("./types").DirectoryUsersResponse>(`/users${query}`),
    enabled,
  });
}

// --- Knowledge pages (#633) ---

export function useKnowledgePages(params?: { tag?: string; q?: string; limit?: number; offset?: number }) {
  const search = new URLSearchParams();
  if (params?.tag) search.set("tag", params.tag);
  if (params?.q) search.set("q", params.q);
  if (params?.limit) search.set("limit", String(params.limit));
  if (params?.offset) search.set("offset", String(params.offset));
  const qs = search.toString();
  return useQuery({
    queryKey: ["knowledge-pages", params],
    queryFn: () => apiFetch<KnowledgePageListResponse>(`/knowledge-pages${qs ? `?${qs}` : ""}`),
  });
}

export function useKnowledgePage(id: string | null) {
  return useQuery({
    queryKey: ["knowledge-page", id],
    queryFn: () => apiFetch<KnowledgePage>(`/knowledge-pages/${id}`),
    enabled: !!id,
  });
}

export function useSearchKnowledgePages(query: string, params?: { limit?: number }) {
  const q = query.trim();
  const search = new URLSearchParams({ q });
  if (params?.limit) search.set("limit", String(params.limit));
  return useQuery({
    queryKey: ["search-knowledge-pages", q, params],
    queryFn: () => apiFetch<ScoredKnowledgePage[]>(`/knowledge-pages/search?${search.toString()}`),
    enabled: q.length >= MIN_SEARCH_LEN,
  });
}

export function useKnowledgePageVersions(id: string | null) {
  return useQuery({
    queryKey: ["knowledge-page-versions", id],
    queryFn: () => apiFetch<KnowledgePageVersionsResponse>(`/knowledge-pages/${id}/versions`),
    enabled: !!id,
  });
}

export function useCreateKnowledgePage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: KnowledgePageInput) =>
      apiFetch<KnowledgePage>(`/knowledge-pages`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["knowledge-pages"] });
    },
  });
}

export function useUpdateKnowledgePage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: KnowledgePageInput }) =>
      apiFetch<KnowledgePage>(`/knowledge-pages/${id}`, { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["knowledge-pages"] });
      void qc.invalidateQueries({ queryKey: ["knowledge-page", vars.id] });
      void qc.invalidateQueries({ queryKey: ["knowledge-page-versions", vars.id] });
    },
  });
}

export function useDeleteKnowledgePage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      // apiFetchRaw does not throw on non-2xx, so check explicitly: a failed
      // delete must reject (not silently fire onSuccess and look deleted).
      const res = await apiFetchRaw(`/knowledge-pages/${id}`, { method: "DELETE" });
      if (!res.ok) {
        throw new Error(`Delete failed (${res.status})`);
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["knowledge-pages"] });
    },
  });
}

/**
 * useResolveRefs resolves a batch of entity-reference URNs (mcp:/urn:li:) to
 * display labels and existence for inline knowledge-page chips (#664). Returns a
 * Map keyed by URN. Disabled when there are no references.
 */
export function useResolveRefs(urns: string[]) {
  // Stable key independent of order/duplication so identical ref sets share a cache entry.
  const key = Array.from(new Set(urns)).sort().join("\n");
  return useQuery({
    queryKey: ["knowledge-page-refs-resolve", key],
    queryFn: () =>
      apiFetch<{ refs: ResolvedRef[] }>("/knowledge-pages/refs/resolve", {
        method: "POST",
        body: JSON.stringify({ urns }),
      }),
    enabled: urns.length > 0,
    staleTime: 60_000,
    select: (data): Map<string, ResolvedRef> => {
      const map = new Map<string, ResolvedRef>();
      for (const r of data.refs) map.set(r.urn, r);
      return map;
    },
  });
}

/**
 * PageEntityRef is a knowledge page's reference, resolved and access-filtered by
 * the server: only references the viewer can access are returned, each with its
 * display label. The id of an inaccessible entity is never included.
 */
export interface PageEntityRef {
  urn: string;
  type: string;
  label: string;
  exists: boolean;
  source: string;
}

/** useKnowledgePageRefs lists a page's stored entity references (#664). */
export function useKnowledgePageRefs(id: string) {
  return useQuery({
    queryKey: ["knowledge-page-refs", id],
    queryFn: () => apiFetch<{ refs: PageEntityRef[] }>(`/knowledge-pages/${id}/refs`),
    enabled: !!id,
  });
}

export interface LineageInsight {
  id: string;
  text: string;
  category: string;
  status: string;
  confidence: string;
  captured_by: string;
}

export interface LineageChangeset {
  id: string;
  change_type: string;
  created_at: string;
  rolled_back: boolean;
  source_insight_ids: string[];
}

export interface KnowledgePageLineage {
  insights: LineageInsight[];
  changesets: LineageChangeset[];
}

/** useKnowledgePageLineage returns the insights a page was synthesized from (#678). */
export function useKnowledgePageLineage(id: string) {
  return useQuery({
    queryKey: ["knowledge-page-lineage", id],
    queryFn: () => apiFetch<KnowledgePageLineage>(`/knowledge-pages/${id}/lineage`),
    enabled: !!id,
  });
}

/**
 * useSetKnowledgePageRefs replaces a page's manual references with the given URNs
 * (promoted/inline refs are preserved server-side). Requires apply_knowledge.
 */
export function useSetKnowledgePageRefs(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (urns: string[]) =>
      apiFetch<{ refs: PageEntityRef[] }>(`/knowledge-pages/${id}/refs`, {
        method: "PUT",
        body: JSON.stringify({ refs: urns }),
      }),
    // Seed the cache from the response so a follow-up edit reads the new set
    // immediately (no stale-closure overwrite between mutations).
    onSuccess: (data) => {
      qc.setQueryData(["knowledge-page-refs", id], data);
    },
  });
}

/** KnowledgeBacklink is a knowledge page that references an entity (reverse lookup). */
export interface KnowledgeBacklink {
  id: string;
  slug: string;
  title: string;
}

/**
 * useKnowledgeBacklinks lists the knowledge pages that reference an entity (#664
 * Phase 4), so an entity view can surface "N knowledge pages reference this". The
 * server returns nothing for an entity the viewer cannot access.
 */
export function useKnowledgeBacklinks(urn: string | undefined) {
  return useQuery({
    queryKey: ["knowledge-backlinks", urn],
    queryFn: () =>
      apiFetch<{ pages: KnowledgeBacklink[] }>(
        `/knowledge-pages/backlinks?urn=${encodeURIComponent(urn ?? "")}`,
      ),
    enabled: !!urn,
  });
}
