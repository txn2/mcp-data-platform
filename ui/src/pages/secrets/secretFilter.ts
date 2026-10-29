import type { Secret } from "@/api/admin/types";

// The Secrets page's search (#2352): what a secret is matched on, and how the
// search is kept in the page's address as ?q= so a filtered list can be linked
// and survives a reload. A secret's value is never read; the server does not
// return one.

export const SEARCH_PARAM = "q";

/** readSearch reads the search text from a query string. */
export function readSearch(search: string): string {
  return new URLSearchParams(search).get(SEARCH_PARAM) ?? "";
}

/** writeSearch sets the search text on params, removing it when empty. */
export function writeSearch(params: URLSearchParams, q: string): URLSearchParams {
  const out = new URLSearchParams(params);
  if (q === "") out.delete(SEARCH_PARAM);
  else out.set(SEARCH_PARAM, q);
  return out;
}

/** matchesSecret reports whether every word of the search text is found,
 * case-insensitively, in the secret's name, description, or the connections
 * and personas it is allowed for. A secret no persona list restricts is
 * allowed for every persona, so a word naming one of `personas` matches it. */
export function matchesSecret(s: Secret, q: string, personas: string[] = []): boolean {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const haystack = [s.name, s.description, ...s.allow_connections, ...s.allow_personas].join("\n").toLowerCase();
  const known = new Set(personas.map((p) => p.toLowerCase()));
  return words.every((w) => haystack.includes(w) || (s.allow_personas.length === 0 && known.has(w)));
}

/** filterSecrets keeps the secrets that match the search text. */
export function filterSecrets(list: Secret[], q: string, personas: string[] = []): Secret[] {
  return list.filter((s) => matchesSecret(s, q, personas));
}
