export type ResourceSort = "updated" | "last_read";

/** Where the library is standing, and what it is narrowed to. */
export interface LibraryView {
  /** The library tab in view. */
  tab: string;
  /** The folder inside it, "" for the library's root. */
  path: string;
  /** The free-text filter, already debounced. It spans the whole library. */
  q: string;
  /** The single tag the view is narrowed to, or "" for every tag. */
  tag: string;
  sort: ResourceSort;
}

/**
 * The library's location lives in the route and its filters in the query
 * string.
 *
 * Which library and which folder is a place: it is what a link points at, what
 * Back steps out of, and what a reload has to return to. `readPath()` in the
 * shell drops the query string, so a location kept there is one the page cannot
 * see on a nav-link push (learned on #1470) -- which is why the tab moved out of
 * `?tab=` and the folder never went in.
 *
 * A filter is not a place. Narrowing a library is something done to the view
 * that is already open, so q, tag and sort stay in the query and are written
 * with replace rather than pushed.
 *
 *   /resources                       the default library, at its root
 *   /resources/lib/global            the Global library, at its root
 *   /resources/lib/global/data       one folder in it
 *   /resources/lib/user/data/shows   a folder inside that one
 *
 * The browse route always carries at least two segments after the section, so
 * it can never be confused with /resources/{id}, which is exactly one.
 */
const BROWSE_SEGMENT = "lib";

/** readLibraryView reads the view out of an in-app location. */
export function readLibraryView(
  location: string,
  basePath: string,
  defaultTab: string,
): LibraryView {
  const [pathname = "", search = ""] = splitLocation(location);
  const sp = new URLSearchParams(search);
  const { tab, path } = readLocation(pathname, basePath, defaultTab);
  return {
    tab,
    path,
    q: sp.get("q") ?? "",
    tag: sp.get("tag") ?? "",
    // An unrecognized order is the default one: this comes off the address bar,
    // where anything at all can be typed, and the sort feeds a query parameter
    // the server validates for itself.
    sort: sp.get("sort") === "last_read" ? "last_read" : "updated",
  };
}

/** splitLocation separates an in-app path from its query string and hash. */
function splitLocation(location: string): [string, string] {
  const withoutHash = location.split("#")[0] ?? "";
  const q = withoutHash.indexOf("?");
  if (q < 0) return [withoutHash, ""];
  return [withoutHash.slice(0, q), withoutHash.slice(q + 1)];
}

/**
 * readLocation pulls the library and folder out of the route.
 *
 * A path that is not a browse route is the default library at its root, which
 * is what the plain section path means. Empty segments are dropped rather than
 * refused: a pasted address with a doubled or trailing slash names the folder
 * the person meant, and there is nothing else it could name.
 */
function readLocation(
  pathname: string,
  basePath: string,
  defaultTab: string,
): { tab: string; path: string } {
  const prefix = `${basePath}/${BROWSE_SEGMENT}/`;
  if (!pathname.startsWith(prefix)) return { tab: defaultTab, path: "" };
  const parts = pathname
    .slice(prefix.length)
    .split("/")
    .map(decodeURIComponent)
    .filter((s) => s !== "");
  const [tab, ...folders] = parts;
  if (!tab) return { tab: defaultTab, path: "" };
  return { tab, path: folders.join("/") };
}

/** libraryPath is the address a view is shown at. */
export function libraryPath(basePath: string, view: LibraryView, defaultTab: string): string {
  const sp = new URLSearchParams();
  if (view.q) sp.set("q", view.q);
  if (view.tag) sp.set("tag", view.tag);
  if (view.sort !== "updated") sp.set("sort", view.sort);
  const qs = sp.toString();

  // The plain section path is the default library at its root, so it stays the
  // address that view is shown at rather than redirecting to a longer one that
  // means the same thing.
  const atDefault = view.tab === defaultTab && view.path === "";
  const route = atDefault
    ? basePath
    : [basePath, BROWSE_SEGMENT, view.tab, ...(view.path ? view.path.split("/") : [])]
        .map((s, i) => (i === 0 ? s : encodeURIComponent(s)))
        .join("/");
  return qs ? `${route}?${qs}` : route;
}

/** folderPath is the address one folder of the library in view is shown at. */
export function folderPath(
  basePath: string,
  view: LibraryView,
  defaultTab: string,
  path: string,
): string {
  // A folder is a place, and a filter narrowing the view it was reached from is
  // not carried into it: opening a folder while a search is running has to show
  // the folder, not the folder minus everything the search excluded.
  return libraryPath(basePath, { ...view, path, q: "", tag: "" }, defaultTab);
}

/**
 * libraryTabFor is the tab a resource's own library is shown under: its persona
 * for a persona-scoped file, and the scope itself otherwise.
 *
 * It is what turns a resource into a place -- the folder trail on a resource's
 * own page has to open the library the file is in, not whichever one the reader
 * last had open.
 */
export function libraryTabFor(scope: string, scopeID: string): string {
  return scope === "persona" ? scopeID : scope;
}

/**
 * folderAddress is the address one folder of one library is shown at, built
 * from nothing but the two of them. It is for a caller with no view of its own
 * to carry, which the resource viewer is.
 */
export function folderAddress(basePath: string, tab: string, path: string): string {
  return [basePath, BROWSE_SEGMENT, tab, ...(path ? path.split("/") : [])]
    .map((s, i) => (i === 0 ? s : encodeURIComponent(s)))
    .join("/");
}
