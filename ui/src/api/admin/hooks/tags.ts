import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TagPage } from "@/api/portal/hooks/bulk";
import { apiFetch } from "../client";

// The tags on every asset, with rename and merge (#2508). Tags compare without
// case: one row covers every spelling, each with its own count.

const KEY = ["admin-tags"] as const;

/** What a rename or merge did. */
export interface RetagResult {
  tag: string;
  moved: number;
}

/** useAdminTags lists every tag beginning with q, most carried first. */
export function useAdminTags(q: string) {
  const query = q.trim();
  return useQuery({
    queryKey: [...KEY, query.toLowerCase()],
    queryFn: () => {
      const sp = new URLSearchParams({ limit: "500" });
      if (query) sp.set("q", query);
      return apiFetch<TagPage>(`/tags?${sp.toString()}`);
    },
    placeholderData: keepPreviousData,
  });
}

function useRetag<T>(path: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: T) => apiFetch<RetagResult>(path, { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
      void qc.invalidateQueries({ queryKey: ["portal-tags"] });
      void qc.invalidateQueries({ queryKey: ["assets"] });
    },
  });
}

/** useRenameTag rewrites one tag, in every spelling, to a new name. */
export function useRenameTag() {
  return useRetag<{ from: string; to: string }>("/tags/rename");
}

/** useMergeTags rewrites the tags named in from, in every spelling, to into. */
export function useMergeTags() {
  return useRetag<{ from: string[]; into: string }>("/tags/merge");
}
