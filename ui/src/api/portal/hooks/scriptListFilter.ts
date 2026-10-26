import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { Script } from "@/api/admin/types";
import type { ListedEndpoint } from "./scriptEndpoint";
import type { ListResponse, ScriptRun, ScriptSchedule } from "./scripts";
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
   * scope is whose scripts to list. "all" is the default and lists every
   * script, where a row the caller does not own carries no source, no
   * fire-time parameters, and a last run without its requester or progress
   * (#1795, #1994); "mine" narrows to the caller's own. An administrator sees
   * everything either way.
   */
  scope?: "mine" | "all";
  /**
   * kind narrows to the libraries ("library") or to the scripts that run
   * ("script", #1941, #1970). Absent lists both.
   */
  kind?: ScriptKind;
  /**
   * endpoint narrows to the scripts bound to an endpoint ("bound") or to the
   * ones that are not ("unbound", #2259). Absent lists both.
   */
  endpoint?: ScriptEndpointFilter;
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
/** ScriptKind is what kind= narrows the listing to. */
export type ScriptKind = "script" | "library";

/** ScriptEndpointFilter is what endpoint= narrows the listing to. */
export type ScriptEndpointFilter = "bound" | "unbound";

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
    // The rows already shown stay while a new order or filter loads, so a
    // click on a column header does not replace the table it clicked with a
    // loading line.
    placeholderData: keepPreviousData,
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
  "kind",
  "endpoint",
  "sort",
  "dir",
] as const satisfies readonly (keyof ScriptListFilter)[];

/**
 * ScriptListResponse is the listing plus the health line above it.
 *
 * total counts every script the predicate matches, so it exceeds data.length
 * when the listing was capped; scheduled counts the same population. failing
 * counts the page, because a run is attached per page -- there is nothing else
 * to count it over.
 */
// PortalScriptRow is one script in the portal listing.
export interface PortalScriptRow {
  script: Script;
  schedule?: ScriptSchedule;
  // last_run is present only for the scripts this caller owns: a run is
  // owner-and-admin reading, and so is the fact that one failed.
  last_run?: ScriptRun;
  // owned reports whether this caller may act on the script and read its
  // runs, so the page offers those surfaces rather than linking to a refusal.
  // A row carries no source for any caller; the script's own route does.
  owned: boolean;
  /** endpoint is the endpoint the script is bound to (#2259), absent when none. */
  endpoint?: ListedEndpoint;
}

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

