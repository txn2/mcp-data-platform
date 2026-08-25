import { resolveRenderer } from "@/components/renderers/registry";
import type { FilterOption } from "@/components/patterns/FilterSelect";
import type { Resource } from "@/api/resources/types";

/**
 * How the library is divided and how each division is drawn.
 *
 * A library fills up with more than prose. A hundred property photographs
 * uploaded for one report used to be a hundred rows of filename, size, and
 * date, with the playbook the same reader keeps in the same library somewhere
 * underneath them (#1471). Grouping puts each kind in its own section, and a
 * section holding only images is shown as images.
 */

/** One category's section of the library. */
export interface ResourceGroup {
  category: string;
  resources: Resource[];
  /**
   * True when every resource in the section is an image, which is what decides
   * a grid over rows. It is read off the content, not off the category name, so
   * a photograph filed under `references` is still shown as a photograph and a
   * written note filed under `visual` is still shown as a row.
   */
  images: boolean;
}

/**
 * Largest resource the grid pulls the original bytes of for a tile.
 *
 * There is no stored thumbnail for a resource, so a tile is the whole object.
 * Without a cutoff, a section of hundred-megabyte photographs is a hundred full
 * downloads; past it the tile stands in for the image until it is opened.
 */
export const TILE_INLINE_LIMIT = 2 * 1024 * 1024; // 2 MB

/**
 * True when this resource renders as an image tile.
 *
 * The registry's own answer, so the grid and the viewer agree on what an image
 * is. SVG is deliberately not one of them: it resolves to its own family, which
 * the viewer sanitizes and renders inline rather than pointing an element at
 * the content endpoint, and the endpoint serves it as an attachment for that
 * same reason. A section of SVG logos is shown as rows.
 */
export function isImageResource(r: Resource): boolean {
  return resolveRenderer({ contentType: r.mime_type, fileName: r.filename }).kind === "image";
}

/** True when a tile must stand in for the image rather than load it. */
export function exceedsTileLimit(r: Resource): boolean {
  return r.size_bytes > TILE_INLINE_LIMIT;
}

/**
 * NEVER_READ_DAYS is how long a resource must have existed unread before the
 * library flags it. A file uploaded yesterday with no reads is not dead weight.
 */
const NEVER_READ_DAYS = 30;

/**
 * True when nothing has read this resource and it is old enough for that to
 * mean something. One rule, read by the table's Last-read column and by the
 * image tile, so an image does not lose the flag by being shown as an image.
 */
export function neverRead(r: Resource): boolean {
  if (r.last_read_at) return false;
  return (Date.now() - new Date(r.created_at).getTime()) / 86_400_000 >= NEVER_READ_DAYS;
}

/**
 * groupByCategory divides the resources in view into their sections.
 *
 * Both orders are the server's. Inside a section the store's order is kept, and
 * the sections themselves follow the order their first member arrived in — so
 * the resource the chosen sort put first is still the first row of the first
 * section. Ordering sections any other way (by a fixed category rank, say)
 * would quietly turn the sort control into a control over nothing: the reader
 * asking for "Recently read" would get category order and no longer be shown
 * what was read most recently.
 */
export function groupByCategory(resources: Resource[]): ResourceGroup[] {
  const byCategory = new Map<string, Resource[]>();
  for (const r of resources) {
    const existing = byCategory.get(r.category);
    if (existing) existing.push(r);
    else byCategory.set(r.category, [r]);
  }

  // Map preserves insertion order, which is first-appearance order.
  return [...byCategory.entries()].map(([category, group]) => ({
    category,
    resources: group,
    images: group.every(isImageResource),
  }));
}

/**
 * tagOptions is the tag facet's choices: the tags carried by the resources in
 * view, plus whichever tag is already selected.
 *
 * The selected one has to be added back, because selecting it narrows the view
 * to the resources carrying it — a facet built from the view alone would drop
 * every other choice the moment one was made, leaving no way back but the
 * unfiltered entry.
 */
export function tagOptions(resources: Resource[], selected: string): FilterOption[] {
  const tags = new Set<string>();
  for (const r of resources) {
    for (const t of r.tags ?? []) tags.add(t);
  }
  if (selected) tags.add(selected);
  return [
    { value: "", label: "All tags" },
    ...[...tags].sort((a, b) => a.localeCompare(b)).map((t) => ({ value: t, label: t })),
  ];
}

// Which sections this reader has folded, remembered so a library with one large
// group and several small ones opens the way it was left. Browser storage, not
// the address bar: it is how one person prefers to read their own library
// rather than part of the view a link carries.
const COLLAPSED_KEY = "resource-library-collapsed";

/** The categories this reader last left folded. */
export function readCollapsed(): string[] {
  try {
    const raw = globalThis.localStorage?.getItem(COLLAPSED_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === "string") : [];
  } catch {
    // Unreadable or unparseable storage means no preference, not a broken page.
    return [];
  }
}

export function writeCollapsed(categories: string[]) {
  try {
    globalThis.localStorage?.setItem(COLLAPSED_KEY, JSON.stringify(categories));
  } catch {
    /* persistence is best-effort */
  }
}
