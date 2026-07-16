import {
  useQuery,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
import type {
  PersonaListResponse,
  PersonaDetail,
  PersonaCreateRequest,
  PersonaTestAccessRequest,
  PersonaTestAccessResult,
  AdminAssetListResponse,
} from "../types";
import type { Asset, AssetVersion, PaginatedResponse } from "@/api/portal/types";
import {
  ASSET_PAGE_SIZE,
  assetKey,
  nextOffset,
  useInfiniteResult,
  type InfiniteAssetsResult,
} from "@/api/portal/hooks/assets";
import { ADMIN_LARGE_ASSET_THRESHOLD } from "./shared";

// ---------------------------------------------------------------------------
// Personas
// ---------------------------------------------------------------------------

export function usePersonas(enabled = true) {
  return useQuery({
    queryKey: ["personas"],
    queryFn: () => apiFetch<PersonaListResponse>("/personas"),
    enabled,
  });
}

export function usePersonaDetail(name: string | null) {
  return useQuery({
    queryKey: ["personas", name],
    queryFn: () => apiFetch<PersonaDetail>(`/personas/${name}`),
    enabled: !!name,
  });
}

export function useCreatePersona() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: PersonaCreateRequest) =>
      apiFetch<PersonaDetail>("/personas", {
        method: "POST",
        body: JSON.stringify(req),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["personas"] });
    },
  });
}

export function useUpdatePersona() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, ...body }: PersonaCreateRequest) =>
      apiFetch<PersonaDetail>(`/personas/${name}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["personas"] });
    },
  });
}

export function useDeletePersona() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch(`/personas/${name}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["personas"] });
    },
  });
}

// Preview a persona's allow/deny decision for a single tool name without
// editing persona rules. Returns matched pattern + source.
export function useTestPersonaAccess() {
  return useMutation({
    mutationFn: ({
      persona,
      toolName,
    }: {
      persona: string;
      toolName: string;
    }) =>
      apiFetch<PersonaTestAccessResult>(
        `/personas/${encodeURIComponent(persona)}/test-access`,
        {
          method: "POST",
          body: JSON.stringify({ tool_name: toolName } as PersonaTestAccessRequest),
        },
      ),
  });
}

// ---------------------------------------------------------------------------
// Assets (admin-scoped)
// ---------------------------------------------------------------------------

interface AdminAssetsParams {
  search?: string;
}

// useInfiniteAdminAssets is the admin-scoped, paginated asset list: it
// accumulates pages so an admin viewing a deployment with more than one page of
// assets can load them all, exposing a single merged page plus the
// fetchNextPage/hasNextPage controls a "Load more" affordance needs.
export function useInfiniteAdminAssets(
  params: AdminAssetsParams = {},
): InfiniteAssetsResult<Asset> {
  // No refetchInterval: an infinite query re-polls every accumulated page on
  // each tick, so a periodic refetch would multiply request volume as the admin
  // loads more. Freshness comes from query invalidation on mutations and
  // window-focus refetch instead.
  const q = useInfiniteQuery({
    queryKey: ["admin", "assets", "infinite", params],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const sp = new URLSearchParams();
      if (params.search) sp.set("search", params.search);
      sp.set("limit", String(ASSET_PAGE_SIZE));
      sp.set("offset", String(pageParam));
      return apiFetch<AdminAssetListResponse>(`/assets?${sp.toString()}`);
    },
    getNextPageParam: (_last, all) => nextOffset(all),
    placeholderData: keepPreviousData,
  });

  return useInfiniteResult(q, assetKey);
}

export function useAdminAsset(id: string | null) {
  return useQuery({
    queryKey: ["admin", "asset", id],
    queryFn: () => apiFetch<Asset>(`/assets/${id}`),
    enabled: !!id,
  });
}

export function useAdminAssetContent(id: string | null, sizeBytes?: number) {
  const tooLarge = sizeBytes != null && sizeBytes > ADMIN_LARGE_ASSET_THRESHOLD;
  return useQuery({
    queryKey: ["admin", "asset-content", id],
    queryFn: async () => {
      const res = await fetch(`/api/v1/admin/assets/${id}/content`, {
        credentials: "include",
      });
      if (!res.ok) throw new Error("Failed to fetch content");
      return res.text();
    },
    enabled: !!id && !tooLarge,
  });
}

export function useAdminUpdateAsset() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: string;
      name?: string;
      description?: string;
      tags?: string[];
    }) =>
      apiFetch(`/assets/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "assets"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset"] });
    },
  });
}

export function useAdminDeleteAsset() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/assets/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "assets"] });
    },
  });
}

export function useAdminUpdateAssetContent() {
  const queryClient = useQueryClient();
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
      queryClient.invalidateQueries({ queryKey: ["admin", "asset-content"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "assets"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset-versions"] });
    },
  });
}

export function useAdminAssetVersions(assetId: string | null) {
  return useQuery({
    queryKey: ["admin", "asset-versions", assetId],
    queryFn: () =>
      apiFetch<PaginatedResponse<AssetVersion>>(
        `/assets/${assetId}/versions`,
      ),
    enabled: !!assetId,
  });
}

export function useAdminVersionContent(assetId: string | null, version: number) {
  return useQuery({
    queryKey: ["admin", "version-content", assetId, version],
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

export function useAdminRevertVersion() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ assetId, version }: { assetId: string; version: number }) =>
      apiFetch(`/assets/${assetId}/versions/${version}/revert`, {
        method: "POST",
      }),
    onSuccess: (_data, { assetId }) => {
      queryClient.invalidateQueries({ queryKey: ["admin", "asset", assetId] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset-content", assetId] });
      queryClient.invalidateQueries({ queryKey: ["admin", "asset-versions", assetId] });
      queryClient.invalidateQueries({ queryKey: ["admin", "assets"] });
    },
  });
}
