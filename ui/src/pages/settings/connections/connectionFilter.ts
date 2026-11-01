import type { EffectiveConnection } from "@/api/admin/types";
import { sortNames } from "@/lib/sortByName";

// The Connections page's search and kind filter (#2350): what a connection is
// matched on, the kinds offered, and how both are kept in the page's address
// so a filtered list can be linked and survives a reload. The address already
// carries the selected connection as ?kind=&name=, so the filter has its own
// parameters, ?q= and ?type=.

export const SEARCH_PARAM = "q";
export const TYPE_PARAM = "type";

/** KIND_LABELS names a connection kind as people say it. */
export const KIND_LABELS: Record<string, string> = {
  trino: "Trino",
  s3: "S3",
  api: "API",
  graphql: "GraphQL",
  mcp: "MCP",
};

/** kindLabel names a kind, the raw kind for one the list does not know. */
export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind;
}

export interface ConnectionFilter {
  q: string;
  type: string;
}

/** readFilter reads the filter from a query string. */
export function readFilter(search: string): ConnectionFilter {
  const params = new URLSearchParams(search);
  return { q: params.get(SEARCH_PARAM) ?? "", type: params.get(TYPE_PARAM) ?? "" };
}

/** writeFilter sets the filter on params, removing a parameter left empty. */
export function writeFilter(params: URLSearchParams, f: ConnectionFilter): URLSearchParams {
  const out = new URLSearchParams(params);
  for (const [key, value] of [
    [SEARCH_PARAM, f.q],
    [TYPE_PARAM, f.type],
  ] as const) {
    if (value === "") out.delete(key);
    else out.set(key, value);
  }
  return out;
}

/** displayName is a connection's display name, when its config carries one. */
function displayName(c: EffectiveConnection): string {
  const v = c.config?.display_name;
  return typeof v === "string" ? v : "";
}

/** matches reports whether a connection matches the search text: every word
 * of it is found, case-insensitively, in the name, the display name, the kind
 * or the description. */
export function matches(c: EffectiveConnection, q: string): boolean {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const haystack = [c.name, c.connection, displayName(c), c.kind, kindLabel(c.kind), c.description ?? ""]
    .join("\n")
    .toLowerCase();
  return words.every((w) => haystack.includes(w));
}

/** filterConnections keeps the connections of the chosen kind that match the
 * search text. An empty kind is every kind. */
export function filterConnections(list: EffectiveConnection[], f: ConnectionFilter): EffectiveConnection[] {
  return list.filter((c) => (f.type === "" || c.kind === f.type) && matches(c, f.q));
}

/** kindOptions are the kinds to filter by: the ones the deployment has, in
 * the order the create form offers them, then any other. A kind the address
 * names that no connection has is offered too, so the filter shows what it
 * holds. */
export function kindOptions(
  list: EffectiveConnection[],
  order: string[],
  current = "",
): { value: string; label: string }[] {
  const present = new Set(list.map((c) => c.kind));
  if (current !== "") present.add(current);
  const kinds = [...order.filter((k) => present.has(k)), ...sortNames([...present].filter((k) => !order.includes(k)))];
  return [{ value: "", label: "All kinds" }, ...kinds.map((k) => ({ value: k, label: kindLabel(k) }))];
}

/** lacksCatalog reports whether an api connection has no catalog linked
 * (#2271): it works by method and path, and none of its operations can be
 * discovered or searched. A connection the platform registers itself (the
 * platform-admin connection, one only the config file declares) is listed
 * without its configuration, so whether it has one is not known here and it
 * is not marked. */
export function lacksCatalog(c: EffectiveConnection): boolean {
  if (c.kind !== "api" || !c.config) return false;
  const id = c.config.catalog_id;
  return typeof id !== "string" || id.trim() === "";
}
