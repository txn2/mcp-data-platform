import {
  useQuery,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { contentLoad } from "@/components/renderers/registry";
import { fetchContentText } from "@/lib/contentFetch";
import { apiFetch, apiFetchRaw } from "../client";
import {
  nextOffset,
  flattenPages,
  useInfiniteResult,
  type InfiniteResult,
} from "./infinite";
import type {
  Asset,
  AssetVersion,
  AssetResponse,
  Share,
  SharedAsset,
  PaginatedResponse,
  ShareResponse,
  ScoredAsset,
  CreateShareBody,
} from "../types";

// Re-exported so existing importers (assets.test.ts and page components) keep
// resolving these from "./assets" after the shared paging primitives moved to
// ./infinite (#972).
export { nextOffset, flattenPages, useInfiniteResult };
export type { InfiniteResult };
// InfiniteAssetsResult is the historical name for the flattened infinite view.
export type InfiniteAssetsResult<T> = InfiniteResult<T>;

// --- Branding (unauthenticated) ---

export interface Branding {
  name: string;
  version: string;
  portal_title: string;
  /** Deployment brand, e.g. "ACME". Empty when the deployment names no brand. */
  brand_name?: string;
  /** Brand home page the portal's brand mark links to. Empty means no link. */
  brand_url?: string;
  /** Link target for the version number in the header. Empty means no link. */
  version_url?: string;
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

/** What useAssetContent needs to know about an asset before it reads the bytes. */
export type AssetContentShape = Pick<Asset, "size_bytes" | "content_type" | "name">;

/**
 * useAssetContent reads an asset's content as text, once its record says the
 * content is worth reading: the registry's contentLoad answers `fetch`, which
 * holds every inline family to its own limit (#1874) and leaves a family whose
 * renderer reads the endpoint itself to that renderer (#1833).
 *
 * It waits for the record. Reading before the size was known meant the limit
 * never applied to the first load, which fetched every large asset whole, and a
 * Parquet file of any size with it.
 *
 * A read that produces no body fails with a ContentFetchError naming the
 * status, which the viewer shows rather than a loading state.
 */
export function useAssetContent(id: string, asset?: AssetContentShape) {
  const fetches = asset != null && contentLoad(asset.content_type, asset.size_bytes, asset.name) === "fetch";
  return useQuery({
    queryKey: ["asset-content", id],
    queryFn: () => fetchContentText(() => apiFetchRaw(`/assets/${id}/content`)),
    enabled: !!id && fetches,
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

// useInfiniteAssets is the paginated counterpart of useAssets: it accumulates
// pages so a caller with more than one page of assets can load them all,
// exposing a single merged page plus fetchNextPage/hasNextPage controls.
export function useInfiniteAssets(params?: {
  content_type?: string;
  tag?: string;
  sort?: string;
  dir?: string;
}): InfiniteAssetsResult<Asset> {
  const q = useInfiniteQuery({
    queryKey: ["assets", "infinite", params],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const sp = new URLSearchParams();
      if (params?.content_type) sp.set("content_type", params.content_type);
      if (params?.tag) sp.set("tag", params.tag);
      // Sent as given; the server resolves both against its own allowlist, so
      // an unknown column orders by the default rather than failing the page.
      if (params?.sort) sp.set("sort", params.sort);
      if (params?.dir) sp.set("dir", params.dir);
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

// useSharedWithMe is one page of the assets other people have shared with this
// reader. It is what the reference picker needs beside useAssets (#1488): the
// server admits a reference to any asset the caller may OPEN, shares included,
// while the asset list is owner-scoped, so a picker built on the list alone
// could never offer an asset the add would have accepted.
export function useSharedWithMe(params?: { limit?: number }) {
  const sp = new URLSearchParams();
  if (params?.limit) sp.set("limit", String(params.limit));
  const qs = sp.toString();

  return useQuery({
    queryKey: ["shared-with-me", params],
    queryFn: () =>
      apiFetch<PaginatedResponse<SharedAsset>>(`/shared-with-me${qs ? `?${qs}` : ""}`),
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
      // null clears the per-asset retention override, returning the asset to
      // the deployment default; omitting the field leaves it as it is.
      max_versions?: number | null;
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

/**
 * useClearAssetThumbnail discards an asset's stored tile, and any failure
 * recorded against it, so the platform's renderer draws it again.
 *
 * It is the way back from a tile that shows the wrong thing (#1497), and the
 * way to ask for another attempt at a document the renderer could not draw
 * (#1787).
 */
export function useClearAssetThumbnail() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/assets/${id}/thumbnail`, { method: "DELETE" }),
    // The asset query is what the thumbnail panel reads to show the tile being
    // drawn and, once it lands, the new one. The refetch is awaited, so the
    // clear is pending until the panel holds the cleared row: between the two,
    // the panel would offer the button again over the tile it just discarded
    // (#1791).
    onSuccess: (_data, id) =>
      Promise.all([
        qc.invalidateQueries({ queryKey: ["asset", id] }),
        qc.invalidateQueries({ queryKey: ["assets"] }),
      ]),
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

export function useCreateShare() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      assetId,
      ...body
    }: CreateShareBody & { assetId: string }) =>
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
