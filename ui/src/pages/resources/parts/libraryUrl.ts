export type ResourceSort = "updated" | "last_read";

/** The part of the library's view that belongs in the address bar. */
export interface LibraryView {
  /** The scope tab in view. */
  tab: string;
  /** The free-text filter, already debounced. */
  q: string;
  category: string;
  /** The single tag the view is narrowed to, or "" for every tag. */
  tag: string;
  sort: ResourceSort;
}

/**
 * The library's view, in the address bar rather than in component state.
 *
 * Opening a resource navigates away from the library, which unmounts it, so
 * anything the library held in state alone is gone by the time the reader
 * presses Back (#1470). Keeping the view in the query string makes Back land on
 * the same scope and the same filters, and makes a narrowed library something
 * that can be linked to.
 *
 * Only what is not at its default is written, so the plain library keeps its
 * plain address.
 */
export function readLibraryView(search: string, defaultTab: string): LibraryView {
  const sp = new URLSearchParams(search);
  return {
    tab: sp.get("tab") || defaultTab,
    q: sp.get("q") ?? "",
    category: sp.get("category") ?? "",
    tag: sp.get("tag") ?? "",
    // An unrecognized order is the default one: this comes off the address bar,
    // where anything at all can be typed, and the sort feeds a query parameter
    // the server validates for itself.
    sort: sp.get("sort") === "last_read" ? "last_read" : "updated",
  };
}

/** libraryPath is the address a view is shown at. */
export function libraryPath(basePath: string, view: LibraryView, defaultTab: string): string {
  const sp = new URLSearchParams();
  if (view.tab !== defaultTab) sp.set("tab", view.tab);
  if (view.q) sp.set("q", view.q);
  if (view.category) sp.set("category", view.category);
  if (view.tag) sp.set("tag", view.tag);
  if (view.sort !== "updated") sp.set("sort", view.sort);
  const qs = sp.toString();
  return qs ? `${basePath}?${qs}` : basePath;
}
