import { MIN_SEARCH_LEN, type EntityRef } from "@/api/portal/datahub";

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
