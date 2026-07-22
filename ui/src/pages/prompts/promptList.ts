import type { Prompt, PromptUsage } from "@/api/admin/types";
import { matchesUsageFacet, usageSortValue, type UsageFacet } from "./promptUsage";

// List model for the two-bucket prompt library (#1010): row shape, facet
// filtering, and sorting, kept out of the page component for testability and
// complexity budgets.

// Row pairs a prompt with its share attribution when it arrived via a
// person-to-person share.
export interface Row {
  prompt: Prompt;
  sharedBy?: string;
}

export type SortKey = "name" | "runs" | "lastRun";
export type SortDir = "asc" | "desc";

export interface Facets {
  collection: string; // collection id, "" = all, "none" = uncollected
  tag: string;
  status: string;
  owner: string;
  usage: UsageFacet;
}

export const allFacets: Facets = { collection: "", tag: "", status: "", owner: "", usage: "all" };

function matchesCollectionFacet(facet: string, p: Prompt): boolean {
  if (!facet) return true;
  if (facet === "none") return !p.collection_id;
  return p.collection_id === facet;
}

export function matchesFacets(row: Row, f: Facets, usage: PromptUsage | undefined): boolean {
  const p = row.prompt;
  return (
    matchesCollectionFacet(f.collection, p) &&
    (!f.tag || (p.tags ?? []).includes(f.tag)) &&
    (!f.status || p.status === f.status) &&
    (!f.owner || p.owner_email === f.owner) &&
    matchesUsageFacet(f.usage, usage)
  );
}

export function facetsActive(f: Facets): boolean {
  return Boolean(f.collection || f.tag || f.status || f.owner || f.usage !== "all");
}

// sortRows orders rows by the chosen key. Each row's key is computed once up
// front (dates parsed, names lowercased O(N) instead of O(N log N) inside the
// comparator), then rows sort on the precomputed key.
export function sortRows(
  rows: Row[],
  key: SortKey,
  dir: SortDir,
  usageMap: Record<string, PromptUsage> | undefined,
): Row[] {
  const decorated = rows.map((row) => ({
    row,
    name: (row.prompt.display_name || row.prompt.name).toLowerCase(),
    usage: key === "name" ? 0 : usageSortValue(key, usageMap?.[row.prompt.id]),
  }));
  decorated.sort((a, b) => {
    const cmp = key === "name" ? a.name.localeCompare(b.name) : a.usage - b.usage;
    return dir === "asc" ? cmp : -cmp;
  });
  return decorated.map((d) => d.row);
}
