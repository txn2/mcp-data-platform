import {
  useQuery,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { AdminCollectionListResponse } from "../types";
import type { Collection } from "@/api/portal/types";
import { COLLECTION_PAGE_SIZE } from "@/api/portal/hooks/collections";
import {
  nextOffset,
  useInfiniteResult,
  type InfiniteResult,
} from "@/api/portal/hooks/infinite";

// ---------------------------------------------------------------------------
// Asset collections (admin-scoped)
//
// The portal list is owner-scoped, so a collection owned by another principal —
// an API-key agent identity nobody can sign in as, most sharply — was reachable
// by nobody at all (#1292). These hooks read the admin routes, which carry no
// owner filter.
// ---------------------------------------------------------------------------

const collectionKey = (c: Collection): string => c.id;

export function useInfiniteAdminCollections(
  params: { search?: string } = {},
): InfiniteResult<Collection> {
  const q = useInfiniteQuery({
    queryKey: ["admin", "collections", "infinite", params],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const sp = new URLSearchParams();
      if (params.search) sp.set("search", params.search);
      sp.set("limit", String(COLLECTION_PAGE_SIZE));
      sp.set("offset", String(pageParam));
      return apiFetch<AdminCollectionListResponse>(`/collections?${sp.toString()}`);
    },
    getNextPageParam: (_last, all) => nextOffset(all),
    placeholderData: keepPreviousData,
  });

  return useInfiniteResult(q, collectionKey);
}

export function useAdminCollection(id: string | null) {
  return useQuery({
    queryKey: ["admin", "collection", id],
    queryFn: () => apiFetch<Collection>(`/collections/${id}`),
    enabled: !!id,
  });
}

export function useAdminUpdateCollection() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; name?: string; description?: string }) =>
      apiFetch<Collection>(`/collections/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "collections"] });
      void queryClient.invalidateQueries({ queryKey: ["admin", "collection"] });
    },
  });
}

export function useAdminDeleteCollection() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiFetch(`/collections/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "collections"] });
    },
  });
}
