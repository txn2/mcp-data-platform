import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
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
} from "../types";

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

// --- Known-users directory for the share picker (#614) ---

export function useDirectoryUsers(q: string, enabled = true) {
  const query = q ? `?q=${encodeURIComponent(q)}` : "";
  return useQuery({
    queryKey: ["portal", "directory-users", q],
    queryFn: () =>
      apiFetch<import("../types").DirectoryUsersResponse>(`/users${query}`),
    enabled,
  });
}
