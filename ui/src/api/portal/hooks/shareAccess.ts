import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "../client";
import type { Share, SharePermission, ShareResponse } from "../types";

// The share dialog's changes to what already exists (#2291): an asset's or a
// collection's one general link, and a grant's permission.

/** The general link an item is asked to have; an omitted field keeps the link's. */
export interface GeneralLinkBody {
  access_mode: "authenticated" | "public";
  /** For a link anyone can open: required to make one, optional to keep one. */
  expires_in?: string;
  notice_text?: string;
  hide_expiration?: boolean;
}

/** The item a general link is for. */
export interface LinkTarget {
  type: "asset" | "collection";
  id: string;
}

function linkPath(t: LinkTarget): string {
  return `/${t.type === "asset" ? "assets" : "collections"}/${encodeURIComponent(t.id)}/link`;
}

function useRefreshShares() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["shares"] });
    void qc.invalidateQueries({ queryKey: ["collection-shares"] });
    void qc.invalidateQueries({ queryKey: ["prompt-shares"] });
  };
}

/** useSetGeneralLink creates the item's general link or changes the one it has, keeping its token. */
export function useSetGeneralLink() {
  const refresh = useRefreshShares();
  return useMutation({
    mutationFn: ({ target, ...body }: GeneralLinkBody & { target: LinkTarget }) =>
      apiFetch<ShareResponse>(linkPath(target), { method: "PUT", body: JSON.stringify(body) }),
    onSuccess: refresh,
  });
}

/** useRemoveGeneralLink revokes the item's general link: Only people added. */
export function useRemoveGeneralLink() {
  const refresh = useRefreshShares();
  return useMutation({
    // The route answers 204 with no body.
    mutationFn: async (target: LinkTarget) => {
      const res = await apiFetchRaw(linkPath(target), { method: "DELETE" });
      if (!res.ok) throw new ApiError(res.status, "The link could not be removed.");
    },
    onSuccess: refresh,
  });
}

/** useSetSharePermission changes a grant's permission in place. */
export function useSetSharePermission() {
  const refresh = useRefreshShares();
  return useMutation({
    mutationFn: ({ shareId, permission }: { shareId: string; permission: SharePermission }) =>
      apiFetch<Share>(`/shares/${encodeURIComponent(shareId)}`, { method: "PATCH", body: JSON.stringify({ permission }) }),
    onSuccess: refresh,
  });
}
