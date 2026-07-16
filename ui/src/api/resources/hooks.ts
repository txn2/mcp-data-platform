import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  useOffsetInfiniteQuery,
  toPaginated,
  type InfiniteResult,
} from "@/api/portal/hooks/infinite";
import { resourceFetch, resourceFetchRaw } from "./client";
import type { Resource, ResourceListResponse, ResourceUpdate } from "./types";

interface ResourceQuery {
  scope?: string;
  scope_id?: string;
  category?: string;
  tag?: string;
  q?: string;
}

function resourceParams(params: ResourceQuery | undefined): URLSearchParams {
  const sp = new URLSearchParams();
  if (params?.scope) sp.set("scope", params.scope);
  if (params?.scope_id) sp.set("scope_id", params.scope_id);
  if (params?.category) sp.set("category", params.category);
  if (params?.tag) sp.set("tag", params.tag);
  if (params?.q) sp.set("q", params.q);
  return sp;
}

export function useResources(params?: ResourceQuery) {
  const qs = resourceParams(params).toString();

  return useQuery({
    queryKey: ["resources", qs],
    queryFn: () => resourceFetch<ResourceListResponse>(`?${qs}`),
  });
}

// RESOURCE_PAGE_SIZE is the number of resources requested per page. The backend
// clamps a client limit to resource.MaxListLimit (200); this stays well under it
// so the library loads incrementally rather than capping at the default page.
export const RESOURCE_PAGE_SIZE = 50;

const resourceKey = (r: Resource): string => r.id;

// useInfiniteResources is the paginated counterpart of useResources: it
// accumulates pages so a deployment with more than one page of resources can
// reach all of them (#972). The list endpoint returns a `{resources,total}`
// envelope, adapted to the shared PaginatedResponse shape here.
export function useInfiniteResources(params?: ResourceQuery): InfiniteResult<Resource> {
  const qs = resourceParams(params).toString();
  return useOffsetInfiniteQuery<Resource>({
    queryKey: ["resources", "infinite", qs],
    pageSize: RESOURCE_PAGE_SIZE,
    keyOf: resourceKey,
    fetchPage: (offset, limit) => {
      const sp = resourceParams(params);
      sp.set("limit", String(limit));
      sp.set("offset", String(offset));
      return resourceFetch<ResourceListResponse>(`?${sp.toString()}`).then((r) =>
        toPaginated(r.resources, r.total, limit, offset),
      );
    },
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
