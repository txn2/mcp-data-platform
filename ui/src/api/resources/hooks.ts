import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  useOffsetInfiniteQuery,
  toPaginated,
  type InfiniteResult,
} from "@/api/portal/hooks/infinite";
import { resourceFetch, resourceFetchRaw } from "./client";
import type {
  FacetsResponse,
  FolderMoveRequest,
  FolderMoveResult,
  Resource,
  ResourceListResponse,
  ResourceUpdate,
  ResourceVersionListResponse,
} from "./types";

interface ResourceQuery {
  scope?: string;
  scope_id?: string;
  // path narrows the listing to one folder and everything beneath it. Absent is
  // the whole library, which is what the root of a tree shows and what a search
  // spans.
  path?: string;
  tag?: string;
  q?: string;
  // sort orders the list; "last_read" puts the most recently read first and
  // never-read resources last, which is how a curator finds dead weight.
  sort?: "updated" | "last_read";
  // limit caps the page. Absent, the server applies its own default (100), and
  // a caller that renders one page has to compare what it got against the
  // envelope's total to know whether it saw everything.
  limit?: number;
}

function resourceParams(params: ResourceQuery | undefined): URLSearchParams {
  const sp = new URLSearchParams();
  if (params?.scope) sp.set("scope", params.scope);
  if (params?.scope_id) sp.set("scope_id", params.scope_id);
  if (params?.path) sp.set("path", params.path);
  if (params?.tag) sp.set("tag", params.tag);
  if (params?.q) sp.set("q", params.q);
  if (params?.sort) sp.set("sort", params.sort);
  if (params?.limit) sp.set("limit", String(params.limit));
  return sp;
}

/**
 * useFacets is what the library in view holds: its folders with exact counts,
 * and the tags its resources carry.
 *
 * Both used to be derived in the browser from the paged listing, so drawing
 * four folder names cost a fetch of the whole library, every count read "25+"
 * until the last page arrived, and the tag filter offered only the tags that
 * page happened to mention (#1555). One request answers both exactly.
 */
export function useFacets(params?: { scope?: string; scope_id?: string }) {
  const sp = new URLSearchParams();
  if (params?.scope) sp.set("scope", params.scope);
  if (params?.scope_id) sp.set("scope_id", params.scope_id);
  const qs = sp.toString();

  return useQuery({
    queryKey: ["resource-facets", qs],
    queryFn: () => resourceFetch<FacetsResponse>(`/facets${qs ? `?${qs}` : ""}`),
  });
}

/** How often a tab asks the server what still needs a capture. */
const THUMBNAIL_PENDING_POLL_MS = 5 * 60_000;

/**
 * usePendingResourceThumbnails lists the resources whose capture is missing or
 * older than the file it came from.
 *
 * The server decides what is pending, from the row rather than from queue
 * state, so a tab does not have to be displaying a resource -- or be on the
 * Resources page at all -- to capture one (#1554).
 */
export function usePendingResourceThumbnails() {
  return useQuery({
    queryKey: ["resource-thumbnails-pending"],
    queryFn: () => resourceFetch<ResourceListResponse>("/thumbnails/pending"),
    refetchInterval: THUMBNAIL_PENDING_POLL_MS,
    refetchOnWindowFocus: true,
    staleTime: THUMBNAIL_PENDING_POLL_MS,
    select: (r) => ({ data: r.resources, total: r.total }),
  });
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
export function useInfiniteResources(
  params?: ResourceQuery,
  // enabled is false on a view that lists no files -- a library root, whose
  // tree comes from the folder endpoint (#1555). The listing used to run there
  // to derive the tree from rows it never displayed.
  enabled = true,
): InfiniteResult<Resource> {
  const qs = resourceParams(params).toString();
  return useOffsetInfiniteQuery<Resource>({
    queryKey: ["resources", "infinite", qs],
    enabled,
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

// useResourceVersions lists a resource's content revisions, newest first.
export function useResourceVersions(id: string, enabled = true) {
  return useQuery({
    queryKey: ["resources", id, "versions"],
    queryFn: () => resourceFetch<ResourceVersionListResponse>(`/${id}/versions`),
    enabled: !!id && enabled,
  });
}

// useReplaceContent uploads new content for an existing resource. The resource
// keeps its ID, URI, and filename, so every reference and prompt attachment
// pointing at it keeps resolving.
export function useReplaceContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, file }: { id: string; file: File }) => {
      const formData = new FormData();
      formData.append("file", file);
      const res = await resourceFetchRaw(`/${id}/content`, { method: "POST", body: formData });
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

// useRestoreVersion re-promotes a prior revision as a new head revision.
export function useRestoreVersion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, version }: { id: string; version: number }) => {
      const res = await resourceFetchRaw(`/${id}/versions/${version}/restore`, { method: "POST" });
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

// useMoveFolder renames a folder, or nests it under another one.
//
// It is a request of its own rather than one PATCH per file because the server
// rewrites the whole subtree in one transaction: a half-renamed folder is not a
// state anyone should be able to observe, and a browser loop over the files
// could not offer that. A refusal means nothing moved.
export function useMoveFolder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: FolderMoveRequest) =>
      resourceFetch<FolderMoveResult>("/folders/move", {
        method: "POST",
        body: JSON.stringify(req),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["resources"] });
    },
  });
}
