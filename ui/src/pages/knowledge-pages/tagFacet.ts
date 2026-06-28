// Tag-facet helpers for the knowledge-pages browse view. Extracted as a pure
// module so the cap/reveal logic is unit-testable without mounting the page.

// TAG_FACET_LIMIT is how many tag chips the facet shows before collapsing the
// rest behind a reveal control. Once a knowledgebase accumulates tags (many of
// them single-use), rendering every chip pushes the page list far down; capping
// keeps the facet compact. Kept as one constant so it is easy to tune (#707).
export const TAG_FACET_LIMIT = 20;

/** A tag and how many pages carry it. */
export type TagCount = [tag: string, count: number];

/**
 * visibleFacetTags returns the tag chips to render. tagCounts must be sorted
 * most-used-first. When collapsed and over the limit, only the top N are shown;
 * a currently-selected tag that falls outside the top N is appended so the
 * active filter is never hidden. When expanded (or at/under the limit) the full
 * list is returned.
 */
export function visibleFacetTags(
  tagCounts: TagCount[],
  selectedTag: string,
  expanded: boolean,
  limit: number = TAG_FACET_LIMIT,
): TagCount[] {
  if (expanded || tagCounts.length <= limit) {
    return tagCounts;
  }
  const top = tagCounts.slice(0, limit);
  if (selectedTag && !top.some(([t]) => t === selectedTag)) {
    const selected = tagCounts.find(([t]) => t === selectedTag);
    if (selected) {
      return [...top, selected];
    }
  }
  return top;
}
