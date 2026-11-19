import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { AssetLinks, AssetMarkEvent, AssetResponse, AssetStatusValue } from "../types";

/**
 * An asset's status (#2502), its Verified badge (#2503) and its links (#2432).
 *
 * A status and a verification grant nothing: who can open an asset is decided
 * by ownership and shares alone. Each change returns the asset as it now reads,
 * which replaces the cached copy so the badges update at once.
 */

/** The query key an asset's status history is cached under. */
export const markHistoryKey = (id: string) => ["asset-mark-history", id] as const;

/** The query key an asset's links are cached under. */
export const assetLinksKey = (id: string) => ["asset-links", id] as const;

function useApplyAsset() {
  const qc = useQueryClient();
  return (asset: AssetResponse | undefined, id: string) => {
    if (asset) {
      // The route answers with the asset; the cached read keeps the caller's
      // own fields (is_owner, share_permission) the route does not repeat.
      qc.setQueryData<AssetResponse>(["asset", id], (prev) => (prev ? { ...prev, ...asset, is_owner: prev.is_owner, share_permission: prev.share_permission } : prev));
    }
    void qc.invalidateQueries({ queryKey: ["asset", id] });
    void qc.invalidateQueries({ queryKey: ["assets"] });
    void qc.invalidateQueries({ queryKey: ["search-assets"] });
    void qc.invalidateQueries({ queryKey: markHistoryKey(id) });
  };
}

/** Sets an asset's status, with an optional note. Owner and editors only. */
export function useSetAssetStatus() {
  const apply = useApplyAsset();
  return useMutation({
    mutationFn: ({ id, status, note }: { id: string; status: AssetStatusValue; note?: string }) =>
      apiFetch<AssetResponse>(`/assets/${encodeURIComponent(id)}/status`, {
        method: "PUT",
        body: JSON.stringify(note ? { status, note } : { status }),
      }),
    onSuccess: (asset, { id }) => apply(asset, id),
  });
}

/** Clears an asset's status. Owner and editors only. */
export function useClearAssetStatus() {
  const apply = useApplyAsset();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<AssetResponse>(`/assets/${encodeURIComponent(id)}/status`, { method: "DELETE" }),
    onSuccess: (asset, id) => apply(asset, id),
  });
}

/** Every change to an asset's status and verification, newest first. */
export function useAssetMarkHistory(id: string, enabled = true) {
  return useQuery({
    queryKey: markHistoryKey(id),
    queryFn: () =>
      apiFetch<{ events: AssetMarkEvent[] }>(`/assets/${encodeURIComponent(id)}/status/history`).then((r) => r.events ?? []),
    enabled: !!id && enabled,
  });
}

/**
 * The end of a chosen day in UTC, as RFC 3339: what the Verify dialog's
 * "On <date>" means. Null for no date.
 */
export function endOfDayUTC(date: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return null;
  return `${date}T23:59:59Z`;
}

/** Marks an asset Verified at its current version. */
export function useVerifyAsset() {
  const apply = useApplyAsset();
  return useMutation({
    mutationFn: ({ id, note, expires_at }: { id: string; note?: string; expires_at?: string | null }) =>
      apiFetch<AssetResponse>(`/assets/${encodeURIComponent(id)}/verification`, {
        method: "PUT",
        body: JSON.stringify({ ...(note ? { note } : {}), ...(expires_at ? { expires_at } : {}) }),
      }),
    onSuccess: (asset, { id }) => apply(asset, id),
  });
}

/** Removes an asset's Verified badge. */
export function useUnverifyAsset() {
  const apply = useApplyAsset();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<AssetResponse>(`/assets/${encodeURIComponent(id)}/verification`, { method: "DELETE" }),
    onSuccess: (asset, id) => apply(asset, id),
  });
}

/**
 * What an asset's current content links to and the assets linking to it the
 * caller can open (#2432). A failed read is an empty answer: the asset page
 * still shows, it just follows no link and lists nothing.
 */
export function useAssetLinks(id: string | undefined, enabled = true) {
  return useQuery({
    queryKey: assetLinksKey(id ?? ""),
    queryFn: () => apiFetch<AssetLinks>(`/assets/${encodeURIComponent(id ?? "")}/links`),
    enabled: !!id && enabled,
    retry: false,
  });
}
