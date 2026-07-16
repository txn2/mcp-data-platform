import {
  useQuery,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  type InfiniteData,
} from "@tanstack/react-query";
import { useMemo } from "react";
import { apiFetch, apiFetchRaw } from "../client";
import type {
  Asset,
  AssetVersion,
  AssetResponse,
  Share,
  SharedAsset,
  PaginatedResponse,
  ShareResponse,
  SharePermission,
  ScoredAsset,
} from "../types";

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

// --- Paginated (infinite) queries ---

// ASSET_PAGE_SIZE is the number of assets requested per page. It matches the
// server-side default and stays well under the API's max limit (200), so the
// asset library loads incrementally rather than capping at a single page.
export const ASSET_PAGE_SIZE = 50;

// assetKey / sharedKey extract the stable identity of a row for de-duplication.
// They are module-level constants so flattenPages memoization stays stable
// across renders. A shared asset is keyed by the underlying asset id.
export const assetKey = (a: Asset): string => a.id;
export const sharedKey = (s: SharedAsset): string => s.asset.id;

// nextOffset returns the offset for the next page: the count of rows already
// fetched (the server offset, not the de-duplicated row count), or undefined
// once every row has been fetched. The cap comes from the LATEST page's total
// so rows inserted after the first fetch are still reachable, and an empty
// trailing page ends pagination even if total is stale-high (rows deleted
// between the count and the fetch), so "Load more" can't spin forever.
export function nextOffset<T>(pages: PaginatedResponse<T>[]): number | undefined {
  const last = pages[pages.length - 1];
  if (last && last.data.length === 0) return undefined;
  const fetched = pages.reduce((n, p) => n + p.data.length, 0);
  return fetched < (last?.total ?? 0) ? fetched : undefined;
}

// InfiniteAssetsResult is the flattened view an infinite asset query exposes to
// list pages: a single accumulated page (all loaded rows merged) plus the
// controls needed to render a "Load more" affordance.
export interface InfiniteAssetsResult<T> {
  data: PaginatedResponse<T> | undefined;
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
}

// flattenPages merges every fetched page into one PaginatedResponse: rows are
// concatenated in fetch order and de-duplicated by keyOf (offset paging over a
// created_at DESC list can re-emit a row when inserts shift the window, which
// would otherwise collide React keys and overcount), total comes from the
// latest page (most current count), limit from the first, and per-asset share
// summaries are unioned across pages. Returns undefined before the first page
// resolves so callers can distinguish "loading" from "empty".
export function flattenPages<T>(
  pages: InfiniteData<PaginatedResponse<T>> | undefined,
  keyOf: (item: T) => string,
): PaginatedResponse<T> | undefined {
  const list = pages?.pages;
  const first = list?.[0];
  if (!list || !first) return undefined;
  const last = list[list.length - 1] ?? first;

  const seen = new Set<string>();
  const data: T[] = [];
  for (const page of list) {
    for (const item of page.data) {
      const k = keyOf(item);
      if (seen.has(k)) continue;
      seen.add(k);
      data.push(item);
    }
  }

  return {
    data,
    total: last.total,
    limit: first.limit,
    offset: 0,
    share_summaries: Object.assign(
      {},
      ...list.map((p) => p.share_summaries ?? {}),
    ),
  };
}

// useInfiniteResult adapts a TanStack infinite query into the flattened
// InfiniteAssetsResult the list pages consume, so useInfiniteAssets,
// useInfiniteSharedWithMe, and the admin variant share one merge/return path.
export function useInfiniteResult<T>(
  q: {
    data: InfiniteData<PaginatedResponse<T>> | undefined;
    isLoading: boolean;
    hasNextPage: boolean;
    isFetchingNextPage: boolean;
    fetchNextPage: () => unknown;
  },
  keyOf: (item: T) => string,
): InfiniteAssetsResult<T> {
  const data = useMemo(() => flattenPages(q.data, keyOf), [q.data, keyOf]);
  return {
    data,
    isLoading: q.isLoading,
    hasNextPage: q.hasNextPage,
    isFetchingNextPage: q.isFetchingNextPage,
    fetchNextPage: () => void q.fetchNextPage(),
  };
}

// useInfiniteAssets is the paginated counterpart of useAssets: it accumulates
// pages so a caller with more than one page of assets can load them all,
// exposing a single merged page plus fetchNextPage/hasNextPage controls.
export function useInfiniteAssets(params?: {
  content_type?: string;
  tag?: string;
}): InfiniteAssetsResult<Asset> {
  const q = useInfiniteQuery({
    queryKey: ["assets", "infinite", params],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const sp = new URLSearchParams();
      if (params?.content_type) sp.set("content_type", params.content_type);
      if (params?.tag) sp.set("tag", params.tag);
      sp.set("limit", String(ASSET_PAGE_SIZE));
      sp.set("offset", String(pageParam));
      return apiFetch<PaginatedResponse<Asset>>(`/assets?${sp.toString()}`);
    },
    getNextPageParam: (_last, all) => nextOffset(all),
  });

  return useInfiniteResult(q, assetKey);
}

// useInfiniteSharedWithMe is the paginated shared-with-me list.
export function useInfiniteSharedWithMe(): InfiniteAssetsResult<SharedAsset> {
  const q = useInfiniteQuery({
    queryKey: ["shared-with-me", "infinite"],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const sp = new URLSearchParams();
      sp.set("limit", String(ASSET_PAGE_SIZE));
      sp.set("offset", String(pageParam));
      return apiFetch<PaginatedResponse<SharedAsset>>(
        `/shared-with-me?${sp.toString()}`,
      );
    },
    getNextPageParam: (_last, all) => nextOffset(all),
  });

  return useInfiniteResult(q, sharedKey);
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
