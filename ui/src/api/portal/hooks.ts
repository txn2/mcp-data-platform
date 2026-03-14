import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "./client";
import type {
  Asset,
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
} from "./types";

// --- Branding (unauthenticated) ---

export interface Branding {
  name: string;
  portal_title: string;
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

export function useAsset(id: string) {
  return useQuery({
    queryKey: ["asset", id],
    queryFn: () => apiFetch<AssetResponse>(`/assets/${id}`),
    enabled: !!id,
  });
}

export function useAssetContent(id: string) {
  return useQuery({
    queryKey: ["asset-content", id],
    queryFn: async () => {
      const res = await apiFetchRaw(`/assets/${id}/content`);
      if (!res.ok) throw new Error("Failed to fetch content");
      return res.text();
    },
    enabled: !!id,
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
    mutationFn: ({ id, content }: { id: string; content: string }) =>
      apiFetch(`/assets/${id}/content`, {
        method: "PUT",
        headers: { "Content-Type": "text/plain" },
        body: content,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["asset-content"] });
      void qc.invalidateQueries({ queryKey: ["asset"] });
      void qc.invalidateQueries({ queryKey: ["assets"] });
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
