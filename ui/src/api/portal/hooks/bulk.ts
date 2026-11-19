import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// Bulk actions on assets (#2507) and the tags a person can see (#2508).
//
// A bulk request names one action and up to MAX_BULK_ASSETS assets; the server
// acts on each through its own single-asset route, so each is authorized as a
// single request would be, and reports which were applied and which skipped.

/** The most assets one bulk request acts on. */
export const MAX_BULK_ASSETS = 500;

/** The reason an asset the caller cannot open, or that does not exist, is skipped. */
export const REASON_HIDDEN = "not found or no access";

export type BulkAction =
  | "add_tags"
  | "remove_tags"
  | "add_to_collection"
  | "set_status"
  | "clear_status"
  | "share"
  | "delete";

export interface BulkShare {
  shared_with_email?: string;
  shared_with_persona?: string;
  permission?: "viewer" | "editor";
}

export interface BulkRequest {
  action: BulkAction;
  asset_ids: string[];
  tags?: string[];
  collection_id?: string;
  section_id?: string;
  status?: string;
  note?: string;
  share?: BulkShare;
}

export interface BulkSkipped {
  asset_id: string;
  reason: string;
}

export interface BulkResult {
  action: BulkAction;
  requested: number;
  applied: string[];
  skipped: BulkSkipped[];
}

/** useBulkAssets runs one bulk action and refreshes every asset list. */
export function useBulkAssets() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: BulkRequest) =>
      apiFetch<BulkResult>("/assets/bulk", { method: "POST", body: JSON.stringify(req) }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["assets"] });
      void qc.invalidateQueries({ queryKey: ["asset"] });
      void qc.invalidateQueries({ queryKey: ["collections"] });
      void qc.invalidateQueries({ queryKey: ["portal-tags"] });
    },
  });
}

/** One way a tag is written, and how many assets carry it so. */
export interface TagSpelling {
  tag: string;
  count: number;
}

/** A tag compared without case: its most used spelling, its count, its spellings. */
export interface TagRow {
  tag: string;
  count: number;
  spellings: TagSpelling[];
}

export interface TagPage {
  tags: TagRow[];
  total: number;
}

/**
 * usePortalTags lists the tags on the assets the caller can open that begin
 * with prefix (without case), most carried first: what the tag input suggests.
 */
export function usePortalTags(prefix: string, enabled = true) {
  const trimmed = prefix.trim();
  return useQuery({
    queryKey: ["portal-tags", trimmed.toLowerCase()],
    queryFn: () => {
      const qs = new URLSearchParams({ limit: "8" });
      if (trimmed) qs.set("prefix", trimmed);
      return apiFetch<TagPage>(`/tags?${qs.toString()}`);
    },
    enabled,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}
