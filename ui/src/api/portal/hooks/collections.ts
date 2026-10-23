import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import {
  useOffsetInfiniteQuery,
  paginatedFetch,
  type InfiniteResult,
} from "./infinite";
import type {
  Share,
  PaginatedResponse,
  ShareResponse,
  SharePermission,
  ScoredCollection,
  Collection,
  CollectionConfig,
  CollectionResponse,
  SharedCollection,
  CreateShareBody,
  CollectionTarget,
  AppendedItem,
} from "../types";

// --- Collections ---

// COLLECTION_PAGE_SIZE matches the portal list default and stays under the API
// max (200) so the collections library loads incrementally rather than capping
// at a single page (#972).
export const COLLECTION_PAGE_SIZE = 50;

const collectionKey = (c: Collection): string => c.id;
const sharedCollectionKey = (s: SharedCollection): string => s.collection.id;

// useInfiniteCollections is the paginated counterpart of useCollections: it
// accumulates pages so a caller with more than one page of collections can load
// them all, exposing a single merged page plus fetchNextPage/hasNextPage.
export function useInfiniteCollections(params?: {
  sort?: string;
  dir?: string;
}): InfiniteResult<Collection> {
  return useOffsetInfiniteQuery<Collection>({
    queryKey: ["collections", "infinite", params],
    pageSize: COLLECTION_PAGE_SIZE,
    keyOf: collectionKey,
    // Sent as given; the server resolves both against its own allowlist, so an
    // unknown column orders by the default rather than failing the page.
    fetchPage: (offset, limit) =>
      paginatedFetch<Collection>("/collections", offset, limit, {
        sort: params?.sort,
        dir: params?.dir,
      }),
  });
}

// useInfiniteSharedCollections is the paginated shared-with-me collections list.
export function useInfiniteSharedCollections(): InfiniteResult<SharedCollection> {
  return useOffsetInfiniteQuery<SharedCollection>({
    queryKey: ["shared-collections", "infinite"],
    pageSize: COLLECTION_PAGE_SIZE,
    keyOf: sharedCollectionKey,
    fetchPage: (offset, limit) =>
      paginatedFetch<SharedCollection>("/shared-collections", offset, limit),
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
    // asset_id makes the asset the new collection's first item (#2260).
    mutationFn: (body: { name: string; description?: string; asset_id?: string }) =>
      apiFetch<Collection>("/collections", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["collections"] });
      void qc.invalidateQueries({ queryKey: ["collection-targets"] });
    },
  });
}

// useCollectionTargets lists the collections the caller can edit, each with its
// sections and whether the asset is already in each (#2260). search narrows by
// name on the server.
export function useCollectionTargets(assetId: string, search: string, enabled = true) {
  const q = search.trim();
  const sp = new URLSearchParams();
  if (q) sp.set("search", q);
  const qs = sp.toString();
  return useQuery({
    queryKey: ["collection-targets", assetId, q],
    enabled: enabled && !!assetId,
    queryFn: () =>
      apiFetch<{ data: CollectionTarget[] }>(
        `/assets/${encodeURIComponent(assetId)}/collection-targets${qs ? `?${qs}` : ""}`,
      ),
  });
}

// useAddToCollection appends one asset to one section of a collection without
// rewriting its other sections (#2260). No section_id adds to the last section.
export function useAddToCollection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ collectionId, assetId, sectionId }: {
      collectionId: string;
      assetId: string;
      sectionId?: string;
    }) =>
      apiFetch<AppendedItem>(`/collections/${encodeURIComponent(collectionId)}/items`, {
        method: "POST",
        body: JSON.stringify({ asset_id: assetId, section_id: sectionId }),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["collection-targets"] });
      void qc.invalidateQueries({ queryKey: ["collection"] });
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
    }: CreateShareBody & { collectionId: string }) =>
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
    }: CreateShareBody & { promptId: string }) =>
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

// --- Known-users directory for the share picker (#614) ---

// DirectoryQuery is how a picker narrows the known-users directory beyond the
// text it matches on.
export interface DirectoryQuery {
  /**
   * confirmedOnly narrows the answer to the people who have actually signed in
   * (#1407), which is what a control that hands something over offers. The
   * narrowing is the server's, so the row cap cannot fill with people who never
   * have and report the rest as nobody.
   */
  confirmedOnly?: boolean;
  /** limit is the page size; the server caps it at 100 and defaults to 50. */
  limit?: number;
}

// useDirectoryUsers reads the known-users directory a picker resolves a person
// from. The response carries the TOTAL as well as the page, so a caller that
// filled its page can say so rather than presenting part of the directory as
// the whole of it.
export function useDirectoryUsers(q: string, enabled = true, opts: DirectoryQuery = {}) {
  const params = new URLSearchParams();
  if (q) params.set("q", q);
  if (opts.confirmedOnly) params.set("confirmed", "true");
  if (opts.limit) params.set("limit", String(opts.limit));
  const rendered = params.toString();
  const query = rendered ? `?${rendered}` : "";
  return useQuery({
    queryKey: ["portal", "directory-users", q, !!opts.confirmedOnly, opts.limit ?? 0],
    queryFn: () =>
      apiFetch<import("../types").DirectoryUsersResponse>(`/users${query}`),
    enabled,
  });
}
