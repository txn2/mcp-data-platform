import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  useOffsetInfiniteQuery,
  toPaginated,
  type InfiniteResult,
} from "@/api/portal/hooks/infinite";
import { resourceFetch, resourceFetchRaw } from "./client";
import type {
  FacetsResponse,
  PeopleResponse,
  Resource,
  ResourceListResponse,
  ResourceSort,
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
  // direct narrows path to its own level: only the files filed at exactly that
  // path, none beneath it. A folder view lists this way, so its page, its total
  // and its Load more all describe the files it shows (#1837).
  direct?: boolean;
  tag?: string;
  q?: string;
  // sort orders the list; "last_read" puts the most recently read first and
  // never-read resources last, which is how a curator finds dead weight, and
  // the rest are the file manager's column sorts (#1872).
  sort?: ResourceSort;
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
  if (params?.path && params.direct) sp.set("direct", "true");
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
export function useFacets(params?: { scope?: string; scope_id?: string }, enabled = true) {
  const sp = new URLSearchParams();
  if (params?.scope) sp.set("scope", params.scope);
  if (params?.scope_id) sp.set("scope_id", params.scope_id);
  const qs = sp.toString();

  // Under the "resources" key, so every write that invalidates the listing
  // redraws the folder tree and its counts too.
  return useQuery({
    queryKey: ["resources", "facets", qs],
    queryFn: () =>
      resourceFetch<FacetsResponse>(`/facets${qs ? `?${qs}` : ""}`),
    enabled,
  });
}

/**
 * usePeople lists every person's resources, for the administrator's People
 * folder (#1872). It is asked only once that folder is opened.
 */
export function usePeople(enabled: boolean) {
  return useQuery({
    queryKey: ["resources", "people"],
    queryFn: () => resourceFetch<PeopleResponse>("/people"),
    enabled,
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
      return resourceFetch<ResourceListResponse>(`?${sp.toString()}`).then(
        (r) => toPaginated(r.resources, r.total, limit, offset),
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

/**
 * useInvalidateResources refreshes every resource query, for a writer that does
 * not go through a mutation here: the many-files upload sends its own requests
 * so it can report progress, and refreshes the library once the batch is done
 * rather than once per file (#1862).
 */
export function useInvalidateResources(): () => Promise<void> {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ["resources"] });
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
    mutationFn: async ({
      id,
      update,
    }: {
      id: string;
      update: ResourceUpdate;
    }) => {
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

/**
 * useClearResourceThumbnail discards a resource's stored tile, and any failure
 * recorded against it, so the platform's renderer draws it again (#1568,
 * #1787). The route clears both variants, which is why this sends no variant.
 */
export function useClearResourceThumbnail() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const res = await resourceFetchRaw(`/${id}/thumbnail`, {
        method: "DELETE",
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }));
        throw new Error(body.error || res.statusText);
      }
    },
    // The resource query is what the thumbnail panel reads; the listing is
    // what draws the tile everywhere else. Both sit under the "resources" key.
    // Awaited for the reason the asset clear awaits its own (#1791).
    onSuccess: () => qc.invalidateQueries({ queryKey: ["resources"] }),
  });
}

// useResourceVersions lists a resource's content revisions, newest first.
export function useResourceVersions(id: string, enabled = true) {
  return useQuery({
    queryKey: ["resources", id, "versions"],
    queryFn: () =>
      resourceFetch<ResourceVersionListResponse>(`/${id}/versions`),
    enabled: !!id && enabled,
  });
}

// useReplaceContent uploads new content for an existing resource. The resource
// keeps its ID, URI, and filename, so every reference and prompt attachment
// pointing at it keeps resolving.
//
// changeSummary says why the content changed and is recorded on the version
// this writes. A file picked from disk says nothing -- the person replaced the
// file, and the version trail already shows that -- while an edit made in the
// viewer names itself, so a reader of the history can tell the two apart.
export function useReplaceContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      id,
      file,
      changeSummary,
    }: {
      id: string;
      file: File;
      changeSummary?: string;
    }) => {
      const formData = new FormData();
      // Before the file: the route stops its walk at the file part, so a field
      // behind it is never read.
      if (changeSummary) {
        formData.append("change_summary", changeSummary);
      }
      formData.append("file", file);
      const res = await resourceFetchRaw(`/${id}/content`, {
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

// useRestoreVersion re-promotes a prior revision as a new head revision.
export function useRestoreVersion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, version }: { id: string; version: number }) => {
      const res = await resourceFetchRaw(`/${id}/versions/${version}/restore`, {
        method: "POST",
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
