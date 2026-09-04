import { useInfiniteQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { ProvenanceCapture } from "../types";

/**
 * Paging an asset's provenance (#1623).
 *
 * A capture is appended on every content write and nothing bounds them, so an
 * asset a scheduled script refreshes hourly gains one an hour. The asset read
 * carries only its newest captures; this reads the rest, newest first, a page
 * at a time.
 */

export interface ProvenancePage {
  captures: ProvenanceCapture[];
  total: number;
  offset: number;
  limit: number;
}

export const provenanceKey = (assetId: string) => [
  "portal",
  "provenance",
  assetId,
];

/** How many captures one page carries. The server's own default and maximum. */
export const PROVENANCE_PAGE_SIZE = 20;

/**
 * useAssetProvenance pages an asset's captures from a starting offset, newest
 * first. `startOffset` is how many the caller already holds from the asset
 * read, so the first page it fetches is the one after those.
 *
 * Disabled until the caller asks, because the captures the asset read carries
 * are all most readers ever look at.
 */
export function useAssetProvenance(
  assetId: string | undefined,
  startOffset: number,
  enabled: boolean,
) {
  return useInfiniteQuery({
    queryKey: [...provenanceKey(assetId ?? ""), startOffset],
    enabled: Boolean(assetId) && enabled,
    initialPageParam: startOffset,
    queryFn: ({ pageParam }) =>
      apiFetch<ProvenancePage>(
        `/assets/${assetId ?? ""}/provenance?offset=${pageParam}&limit=${PROVENANCE_PAGE_SIZE}`,
      ),
    getNextPageParam: (last: ProvenancePage) => {
      const next = last.offset + last.captures.length;
      return next < last.total ? next : undefined;
    },
  });
}
