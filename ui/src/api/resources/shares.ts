import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Share, SharePermission } from "@/api/portal/types";
import type { Resource } from "./types";
import { resourceFetch, resourceFetchRaw } from "./client";

// Sharing a file (#2258): with a person by address or with a persona, as
// viewer or editor; posting it to a channel; and the files shared with the
// caller.

export interface ResourceShareBody {
  resourceId: string;
  shared_with_email?: string;
  shared_with_persona?: string;
  permission?: SharePermission;
  message?: string;
  notify?: boolean;
}

export interface SharedResource {
  resource: Resource;
  share_id: string;
  shared_by: string;
  shared_at: string;
  permission: SharePermission;
  via_persona?: string;
}

export function useResourceShares(resourceId: string) {
  return useQuery({
    queryKey: ["resources", resourceId, "shares"],
    queryFn: () => resourceFetch<{ shares: Share[] }>(`/${encodeURIComponent(resourceId)}/shares`).then((r) => r.shares),
    enabled: resourceId !== "",
  });
}

export function useCreateResourceShare() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ resourceId, ...body }: ResourceShareBody) =>
      resourceFetch<Share>(`/${encodeURIComponent(resourceId)}/shares`, { method: "POST", body: JSON.stringify(body) }),
    onSuccess: (_s, { resourceId }) => qc.invalidateQueries({ queryKey: ["resources", resourceId, "shares"] }),
  });
}

export function useRevokeResourceShare(resourceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (shareId: string) => {
      const res = await resourceFetchRaw(`/${encodeURIComponent(resourceId)}/shares/${encodeURIComponent(shareId)}`, {
        method: "DELETE",
      });
      if (!res.ok) throw new Error("The share could not be removed.");
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["resources", resourceId, "shares"] }),
  });
}

/** useSetResourceSharePermission changes a file share's permission in place (#2291). */
export function useSetResourceSharePermission(resourceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ shareId, permission }: { shareId: string; permission: SharePermission }) =>
      resourceFetch<Share>(`/${encodeURIComponent(resourceId)}/shares/${encodeURIComponent(shareId)}`, {
        method: "PATCH",
        body: JSON.stringify({ permission }),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["resources", resourceId, "shares"] }),
  });
}

/** The files shared with the caller, directly or through their persona. */
export function useSharedResources(enabled = true) {
  return useQuery({
    queryKey: ["resources", "shared-with-me"],
    queryFn: () => resourceFetch<{ resources: SharedResource[] }>("/shared-with-me").then((r) => r.resources),
    enabled,
  });
}

export interface ResourceChannelBody {
  resourceId: string;
  channel: string;
  target?: string;
  /** authenticated shares the file with every signed-in internal person; existing posts its page. */
  access_mode: "authenticated" | "existing";
  message?: string;
}

/** Posts a file to a notification channel. */
export function usePostResourceToChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ resourceId, ...body }: ResourceChannelBody) =>
      resourceFetch<Record<string, unknown>>(`/${encodeURIComponent(resourceId)}/channel-shares`, {
        method: "POST",
        body: JSON.stringify({ ...body, target: body.target || undefined, message: body.message || undefined }),
      }),
    onSuccess: (_r, { resourceId }) => qc.invalidateQueries({ queryKey: ["resources", resourceId, "shares"] }),
  });
}
