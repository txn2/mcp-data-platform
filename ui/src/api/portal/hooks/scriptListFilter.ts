import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { ListResponse, PortalScriptRow } from "./scripts";
import { scriptsKey } from "./scriptKeys";

// How the scripts listing is narrowed and ordered, and the response it gets.
// Its own file for the scripts hook module's line budget; the listing filter
// is one subject, and every axis on it is applied by the server (#1795).

// ScriptListFilter narrows the listing to one category, one tag, free text, or
// any combination (#1369, #1405). Every axis is applied by the server rather
// than in the table, so the answer is the same one an agent's list gets, and a
// filtered page does not depend on having already loaded every row — the
// listing is capped, and a page that filtered its own rows would answer from a
// truncated set while reporting a count to match.
export interface ScriptListFilter {
  category?: string;
  tag?: string;
  /** search matches a script's name, display name, or description. */
  search?: string;
  /** owner narrows to one author, by email. */
  owner?: string;
  /** status narrows to one lifecycle status. */
  status?: string;
  /** enabled narrows to the enabled or the disabled scripts. */
  enabled?: boolean;
  /**
   * scope is whose scripts to list. "mine" is the default and is what this
   * listing has always shown; "all" lists every script on the platform, where
   * a row the caller does not own carries no source and no run state (#1795).
   * An administrator sees everything either way.
   */
  scope?: "mine" | "all";
  /**
   * sort and dir order the listing IN THE STORE. Ordering in the browser
   * would order the page the cap returned, so "A-Z" would silently mean "A-Z
   * within the most recently updated 200.
   */
  sort?: ScriptSortKey;
  dir?: "asc" | "desc";
}

/**
 * ScriptSortKey is a column the server will order by. Last run is absent on
 * purpose: it is attached per page after the query, so ordering by it would
 * order the page rather than the listing.
 */
export type ScriptSortKey = "name" | "display_name" | "owner_email" | "created_at" | "updated_at";

// useScriptListing reads the scripts this caller may see: their own, and every
// script on the platform for an administrator, which is what the
// administrator's section lists (#1407). One listing serves both surfaces
// because the server answers both from one predicate; a second endpoint for
// the admin page would be a second answer to the same question.
export function useScriptListing(filter: ScriptListFilter = {}) {
  const query = scriptListQuery(filter);
  return useQuery({
    // The key is the axes in a fixed order, so two filters that differ in any
    // one of them are two cache entries and neither can serve the other.
    queryKey: [...scriptsKey, "list", ...LIST_AXES.map((axis) => filter[axis] ?? "")],
    queryFn: () => apiFetch<ScriptListResponse>(`/scripts${query}`),
  });
}

/**
 * The axes a listing is narrowed and ordered by, in one place: the query key
 * and the query string are each one entry per axis, and a new axis added to
 * only one of them would be a filter the cache could not tell apart.
 */
const LIST_AXES = [
  "category",
  "tag",
  "search",
  "owner",
  "status",
  "enabled",
  "scope",
  "sort",
  "dir",
] as const satisfies readonly (keyof ScriptListFilter)[];

/**
 * ScriptListResponse is the listing plus the health line above it.
 *
 * total counts every script the predicate matches, so it exceeds data.length
 * when the listing was capped; scheduled counts the same population. failing
 * counts the page, because a run is attached per page and only for the rows
 * the caller owns -- there is nothing else to count it over.
 */
export interface ScriptListResponse extends ListResponse<PortalScriptRow> {
  scheduled: number;
  failing: number;
}

// scriptListQuery renders the filter as a query string, empty when nothing is
// filtered so the unfiltered request stays the plain one.
export function scriptListQuery(filter: ScriptListFilter): string {
  const params = new URLSearchParams();
  for (const axis of LIST_AXES) {
    const value = filter[axis];
    // `false` is a value here, not an absence: enabled=false narrows to the
    // disabled scripts, and a plain truthiness test would drop it.
    if (value === undefined || value === "") continue;
    params.set(axis, String(value));
  }
  const rendered = params.toString();
  return rendered ? `?${rendered}` : "";
}

