import {
  useQuery,
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
import { REFETCH_INTERVAL, ADMIN_LARGE_ASSET_THRESHOLD } from "./shared";

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
  limit?: number;
  offset?: number;
}

export function useAdminAssets(params: AdminAssetsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.search) searchParams.set("search", params.search);
  if (params.limit) searchParams.set("limit", String(params.limit));
  if (params.offset) searchParams.set("offset", String(params.offset));

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["admin", "assets", params],
    queryFn: () =>
      apiFetch<AdminAssetListResponse>(
        `/assets${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
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
