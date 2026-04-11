import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { resourceFetch, resourceFetchRaw } from "./client";
import type { Resource, ResourceListResponse, ResourceUpdate } from "./types";

export function useResources(params?: {
  scope?: string;
  scope_id?: string;
  category?: string;
  tag?: string;
  q?: string;
}) {
  const searchParams = new URLSearchParams();
  if (params?.scope) searchParams.set("scope", params.scope);
  if (params?.scope_id) searchParams.set("scope_id", params.scope_id);
  if (params?.category) searchParams.set("category", params.category);
  if (params?.tag) searchParams.set("tag", params.tag);
  if (params?.q) searchParams.set("q", params.q);
  const qs = searchParams.toString();

  return useQuery({
    queryKey: ["resources", qs],
    queryFn: () => resourceFetch<ResourceListResponse>(`?${qs}`),
  });
}

export function useResource(id: string) {
  return useQuery({
    queryKey: ["resources", id],
    queryFn: () => resourceFetch<Resource>(`/${id}`),
    enabled: !!id,
  });
}

export function useUploadResource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (formData: FormData) => {
      const res = await resourceFetchRaw("", {
        method: "POST",
        body: formData,
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }));
        throw new Error(body.error || res.statusText);
      }
      return res.json() as Promise<Resource>;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["resources"] });
    },
  });
}

export function useUpdateResource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, update }: { id: string; update: ResourceUpdate }) => {
      return resourceFetch<Resource>(`/${id}`, {
        method: "PATCH",
        body: JSON.stringify(update),
      });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["resources"] });
    },
  });
}

export function useDeleteResource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const res = await resourceFetchRaw(`/${id}`, { method: "DELETE" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }));
        throw new Error(body.error || res.statusText);
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["resources"] });
    },
  });
}
