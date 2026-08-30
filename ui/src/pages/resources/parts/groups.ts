import { resolveRenderer } from "@/components/renderers/registry";
import type { FilterOption } from "@/components/patterns/FilterSelect";
import type { Resource } from "@/api/resources/types";

/**
 * How a folder's files are drawn, and the two flags a row and a tile share.
 *
 * A library fills up with more than prose. A hundred property photographs
 * uploaded for one report used to be a hundred rows of filename, size, and
 * date, with the playbook the same reader keeps in the same library somewhere
 * underneath them (#1471). A folder holding only images is shown as images.
 */

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
 * tagOptions is the tag facet's choices: every tag the library holds, plus
 * whichever is already selected.
 *
 * The tags are the library's, reported by the facets endpoint, rather than the
 * ones a loaded page happens to carry (#1555): a facet built from the page was
 * empty at a library root, where no page is loaded at all, and short everywhere
 * else. The selected one is added back because it may name a tag no longer in
 * use, and a facet that dropped it would leave no way back but the unfiltered
 * entry.
 */
export function tagOptions(libraryTags: string[], selected: string): FilterOption[] {
  const tags = new Set<string>(libraryTags);
  if (selected) tags.add(selected);
  return [
    { value: "", label: "All tags" },
    ...[...tags].sort((a, b) => a.localeCompare(b)).map((t) => ({ value: t, label: t })),
  ];
}
