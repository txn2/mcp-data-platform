import { MIN_SEARCH_LEN, type EntityRef } from "@/api/portal/datahub";
import { catalogSubForURN, type CatalogSubTab } from "@/lib/entityRefs";

export const MIN_SEARCH = MIN_SEARCH_LEN;

export function shortUrn(urn: string): string {
  const parts = urn.split(":");
  return parts[parts.length - 1] || urn;
}

// filterDomains narrows the full domain list to those whose name or URN contains
// the query (case-insensitive); an empty query returns the whole list so a
// focused picker shows every domain.
export function filterDomains(domains: EntityRef[], query: string): EntityRef[] {
  const q = query.trim().toLowerCase();
  if (!q) return domains;
  return domains.filter(
    (d) => d.name.toLowerCase().includes(q) || d.urn.toLowerCase().includes(q),
  );
}

/** CATALOG_URN_PARAM is the query parameter that deep-links one catalog entity. */
export const CATALOG_URN_PARAM = "urn";

/** urnFromLocation reads the deep-linked entity URN, if any. Only a well-formed
 * `urn:` value is accepted, so nothing else in the query string can be coerced
 * into a catalog lookup. */
function urnFromLocation(): string | null {
  if (typeof window === "undefined") return null;
  const value = new URLSearchParams(window.location.search).get(CATALOG_URN_PARAM);
  return value && value.startsWith("urn:") ? value : null;
}

/**
 * deepLinkedURN returns the deep-linked entity URN when it is one the given
 * inner tab can open, and null otherwise (#1159).
 *
 * Every Catalog inner tab reads the entity from the same query parameter, and
 * the kind of URN decides which tab shows it, so each tab claims only its own
 * kinds. A glossary-term URN arriving with the Tables hash — a hand-edited or
 * stale link — therefore opens the Tables list rather than a table read that
 * cannot succeed.
 */
export function deepLinkedURN(sub: CatalogSubTab): string | null {
  const urn = urnFromLocation();
  return urn && catalogSubForURN(urn) === sub ? urn : null;
}

/** clearURNFromLocation drops the deep link when the reader goes back to the
 * list, so a refresh does not reopen the entity they just left. */
export function clearURNFromLocation() {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  if (!url.searchParams.has(CATALOG_URN_PARAM)) return;
  url.searchParams.delete(CATALOG_URN_PARAM);
  window.history.replaceState(window.history.state, "", url.toString());
}

// withRawUrn prepends the typed query as a candidate when it is itself a
// well-formed `urn:li:<type>:<id>` URN not already in the list, so an exact URN a
// user pastes (or a value name search cannot surface) stays applicable.
export function withRawUrn(candidates: EntityRef[], query: string, type: string): EntityRef[] {
  const q = query.trim();
  const prefix = `urn:li:${type}:`;
  if (!q.startsWith(prefix) || q.length <= prefix.length) return candidates;
  if (candidates.some((c) => c.urn === q)) return candidates;
  return [{ urn: q, name: q }, ...candidates];
}
