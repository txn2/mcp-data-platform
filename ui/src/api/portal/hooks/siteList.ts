import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { SiteList } from "./sites";

// The Sites list searched and paged on the server, and an environment's tile
// (#2658).

/** SITES_PAGE is how many sites one page of the list holds. */
export const SITES_PAGE = 25;

/** useSitePage is the sites q names, one page from offset; total counts
 * every match. Its key sits under the sites key, so every site change
 * refreshes it. */
export function useSitePage(q: string, offset: number) {
  const params = new URLSearchParams({ limit: String(SITES_PAGE), offset: String(offset) });
  if (q) params.set("q", q);
  return useQuery({
    queryKey: ["sites", "list", q, offset],
    queryFn: () => apiFetch<SiteList>(`/sites?${params.toString()}`),
    placeholderData: keepPreviousData,
  });
}

/** siteTileURL is where an environment's tile is read. */
export function siteTileURL(site: string, environment: string): string {
  return `/api/v1/portal/sites/${encodeURIComponent(site)}/environments/${encodeURIComponent(environment)}/tile`;
}
