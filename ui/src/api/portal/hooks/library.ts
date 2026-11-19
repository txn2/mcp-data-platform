import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "../client";

// A person's library (#2504, #2509): what they opened, what they starred, and
// the trash, unused assets and possible duplicates they tidy their assets
// from. Every list is read by the server under the reach rule, so an item the
// caller can no longer open is simply absent.

export type LibraryKind = "asset" | "collection";

export interface LibraryItem {
  kind: LibraryKind;
  id: string;
  name: string;
  content_type?: string;
  owner_email?: string;
  /** When last opened (recents) or starred (favorites). */
  at: string;
}

export interface TrashItem {
  kind: LibraryKind;
  id: string;
  name: string;
  content_type?: string;
  owner_email?: string;
  deleted_at: string;
  purge_at: string;
}

export interface TrashPage {
  items: TrashItem[];
  total: number;
  retention_days: number;
}

export interface UnusedItem {
  id: string;
  name: string;
  content_type?: string;
  owner_email?: string;
  status?: string;
  opens: number;
  last_opened?: string;
  created_at: string;
}

export interface UnusedPage {
  items: UnusedItem[];
  total: number;
  days: number;
}

export interface DuplicateMember {
  id: string;
  name: string;
  owner_email?: string;
  version: number;
  updated_at: string;
  status?: string;
  opens: number;
}

export interface DuplicateGroup {
  digest: string;
  similarity: number;
  members: DuplicateMember[];
}

/** A write the server answers 204 to: no body to parse. */
async function send(path: string, method: string, body?: unknown): Promise<void> {
  const res = await apiFetchRaw(path, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const detail = await res.json().catch(() => ({ detail: res.statusText }));
    throw new ApiError(res.status, detail.detail || res.statusText, detail);
  }
}

const recentsKey = ["library", "recents"] as const;
const favoritesKey = ["library", "favorites"] as const;

/** The items the caller opened, newest first. */
export function useRecents(limit = 20, enabled = true) {
  return useQuery({
    queryKey: [...recentsKey, limit],
    queryFn: () => apiFetch<{ items: LibraryItem[] }>(`/recents?limit=${limit}`).then((r) => r.items),
    enabled,
  });
}

/** The items the caller starred, newest first. */
export function useFavorites(limit = 50, enabled = true) {
  return useQuery({
    queryKey: [...favoritesKey, limit],
    queryFn: () => apiFetch<{ items: LibraryItem[] }>(`/favorites?limit=${limit}`).then((r) => r.items),
    enabled,
  });
}

/** Whether an item is among the caller's favorites, read from the list. */
export function useIsFavorite(kind: LibraryKind, id: string): boolean {
  const { data } = useFavorites();
  return !!data?.some((f) => f.kind === kind && f.id === id);
}

/** Stars or un-stars an item. */
export function useSetFavorite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, id, on }: { kind: LibraryKind; id: string; on: boolean }) =>
      send(`/favorites/${kind}/${encodeURIComponent(id)}`, on ? "PUT" : "DELETE"),
    onSuccess: () => qc.invalidateQueries({ queryKey: favoritesKey }),
  });
}

/** Reports that the caller opened an item. Failures are ignored: an open that
 *  goes unrecorded costs a Recent entry, never the page. */
export function recordOpen(kind: LibraryKind, id: string): Promise<void> {
  return send("/recents", "POST", { kind, id }).catch(() => undefined);
}

/** Reports the open of an item once it has loaded (id set), and again when
 *  the page moves to another item. */
export function useRecordOpen(kind: LibraryKind, id: string | undefined) {
  useEffect(() => {
    if (id) void recordOpen(kind, id);
  }, [kind, id]);
}

const everyoneQS = (everyone: boolean) => (everyone ? "&everyone=true" : "");

export function useTrash(everyone: boolean) {
  return useQuery({
    queryKey: ["library", "trash", everyone],
    queryFn: () => apiFetch<TrashPage>(`/trash?limit=200${everyoneQS(everyone)}`),
  });
}

export function useRestore() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, id }: { kind: LibraryKind; id: string }) =>
      apiFetch<{ status: string }>(`/trash/${kind}/${encodeURIComponent(id)}/restore`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["library"] }),
  });
}

export function usePurge() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, id }: { kind: LibraryKind; id: string }) =>
      apiFetch<{ status: string }>(`/trash/${kind}/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["library", "trash"] }),
  });
}

export function useUnused(days: number, everyone: boolean) {
  return useQuery({
    queryKey: ["library", "unused", days, everyone],
    queryFn: () => apiFetch<UnusedPage>(`/unused?days=${days}&limit=200${everyoneQS(everyone)}`),
  });
}

export function useDuplicates(everyone: boolean) {
  return useQuery({
    queryKey: ["library", "duplicates", everyone],
    queryFn: () =>
      apiFetch<{ groups: DuplicateGroup[] }>(`/duplicates${everyone ? "?everyone=true" : ""}`).then((r) => r.groups),
  });
}

export function useDismissGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ digest, undo }: { digest: string; undo?: boolean }) =>
      undo ? send(`/duplicates/dismissals/${digest}`, "DELETE") : send("/duplicates/dismissals", "POST", { digest }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["library", "duplicates"] }),
  });
}

/** Deletes an asset (to the trash), the existing single-asset route. */
export function useTrashAsset() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiFetch<unknown>(`/assets/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["library"] }),
  });
}

/** Sets an asset Deprecated with a note, through the asset status route (#2502). */
export function useDeprecateAsset() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, note }: { id: string; note: string }) =>
      apiFetch<unknown>(`/assets/${encodeURIComponent(id)}/status`, {
        method: "PUT",
        body: JSON.stringify({ status: "deprecated", note }),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["library"] }),
  });
}
