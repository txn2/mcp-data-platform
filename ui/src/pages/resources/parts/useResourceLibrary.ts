import { useCallback, useEffect, useRef, useState } from "react";
import { useFacets, useInfiniteResources } from "@/api/resources/hooks";
import type { InfiniteResult } from "@/api/portal/hooks/infinite";
import type { Resource } from "@/api/resources/types";
import { ALL_LIBRARIES } from "../scopes";
import {
  folderPath,
  libraryPath,
  readLibraryView,
  type LibraryView,
  type ResourceSort,
} from "./libraryUrl";

export type { ResourceSort } from "./libraryUrl";

/**
 * scopeParams narrows a query to the library the picker names. The All entry
 * sends no scope at all, which is what makes it unfiltered: the server answers
 * it with every library the caller may read (resource.ListScopes).
 *
 * Exported because the recently-updated strip asks the same question of the
 * same library through its own request, and the two have to be narrowed
 * identically or the strip would report a library nobody is looking at.
 */
export function scopeParams(activeTab: string): Record<string, string> {
  if (activeTab === ALL_LIBRARIES) return {};
  if (activeTab === "user" || activeTab === "global") return { scope: activeTab };
  return { scope: "persona", scope_id: activeTab };
}

// liveScope keeps the selection on a library that still exists. The persona
// scopes come from a live list, so the one in view can disappear under the
// reader; falling back to the first keeps the query and the picker agreeing
// instead of leaving the page with nothing selected and nothing shown.
function liveScope(selected: string, scopes: string[]): string {
  return scopes.includes(selected) ? selected : (scopes[0] ?? selected);
}

interface LibrarySection {
  /** Where this library is mounted: "/resources" or "/admin/resources". */
  basePath: string;
  /**
   * The in-app location the shell is showing, query string included. The
   * library and folder in view are read out of it rather than held in state,
   * which is what makes Back step out of a folder and a pasted link open one.
   */
  location: string;
  /** The shell's navigate, used to move between locations and views. */
  onNavigate?: (path: string, opts?: { replace?: boolean }) => void;
}

/**
 * useResourceLibrary owns what the library is showing and the page of resources
 * that follows from it.
 *
 * The location -- which library, which folder -- is derived from the route on
 * every render rather than mirrored into state, so browser Back, a reload and a
 * pasted link all arrive at the same view with no synchronization to get wrong.
 * Moving is a navigation.
 *
 * The free-text box is the one piece of local state: it is debounced against
 * the query rather than the input, so typing does not fire a request per
 * keystroke, and the settled value is written back to the address with replace.
 *
 * A search spans the whole library rather than the open folder. A search that
 * only looked in the folder someone was standing in would make the tree worse
 * than the flat list it replaced, so the folder filter is dropped while a query
 * is running and each hit carries the path it was found at.
 *
 * `scopes` is the set of libraries the caller may look at; the query is clamped
 * to it (see liveScope). The clamp does not reach the address bar: the persona
 * scopes arrive asynchronously, so a deep link naming one is briefly outside the
 * set, and canonicalizing off the clamped value would discard the link before
 * its scope had loaded.
 */
export function useResourceLibrary(scopes: string[], section: LibrarySection) {
  // Both pages open on All. A reader's libraries are few and mostly hold other
  // people's material, so the useful first view is all of it at once (#1553);
  // narrowing to one is the deliberate act, and it is a place, so it is in the
  // address and Back returns from it.
  const defaultTab = ALL_LIBRARIES;
  const { basePath, location, onNavigate } = section;
  const view = readLibraryView(location, basePath, defaultTab);
  const activeTab = liveScope(view.tab, scopes);

  const [searchInput, setSearchInput] = useState(view.q);
  const settledSearch = useDebounced(searchInput);

  // Two directions, and neither may drive the other into a loop. A settled
  // query different from the address writes the address; an address different
  // from the box -- a Back, a pasted link, a folder opened while typing --
  // refills the box.
  useEffect(() => {
    if (settledSearch === view.q) return;
    onNavigate?.(libraryPath(basePath, { ...view, q: settledSearch }, defaultTab), {
      replace: true,
    });
    // The view is rebuilt every render; depending on it would rewrite the
    // address on every keystroke of an unrelated field.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settledSearch]);

  useEffect(() => {
    if (view.q !== settledSearch) setSearchInput(view.q);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view.q]);

  const go = useCallback(
    (next: Partial<typeof view>, opts?: { replace?: boolean }) => {
      onNavigate?.(libraryPath(basePath, { ...view, ...next }, defaultTab), opts);
    },
    // view is rebuilt per render, so the callback is too; it is read at call
    // time either way and every consumer is an event handler.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [basePath, defaultTab, onNavigate, location],
  );

  const { searching, flat, listing } = viewShape(view);
  // The tree comes from the server, exactly, in one request (#1555).
  const facets = useFacets(scopeParams(activeTab));

  const query = useInfiniteResources(listingFor(view, activeTab, searching), listing);

  return {
    /** The library in view, clamped to one the caller may look at. */
    activeTab,
    /** The folder in view, "" at the library's root. */
    path: view.path,
    searching,
    /**
     * True when the view is a flat list of hits from across the library rather
     * than one level of its tree: a search, or a tag chosen at the root.
     */
    flat,
    searchInput,
    setSearchInput,
    tag: view.tag,
    setTag: (tag: string) => go({ tag }, { replace: true }),
    sort: view.sort,
    setSort: (sort: ResourceSort) => go({ sort }, { replace: true }),
    /** Opening a library is a place, so it is pushed and Back returns here. */
    setActiveTab: (tab: string) => go({ tab, path: "", q: "", tag: "" }),
    /** Opening a folder is a place too, which is what makes Back step out. */
    openFolder: (path: string) => onNavigate?.(folderPath(basePath, view, defaultTab, path)),
    /** The address of one folder, for a link and for the entry being left. */
    addressOf: (path: string) => folderPath(basePath, view, defaultTab, path),
    /** Where this view lives, search box included even mid-debounce. */
    address: libraryPath(basePath, { ...view, q: searchInput }, defaultTab),
    ...pageOf(query, listing),
    /** True while the view in hand is still being fetched. */
    isLoading: listing ? query.isLoading : facets.isLoading,
    /** The library's folders, exact, whatever level is on screen. */
    folders: facets.data?.folders ?? [],
    /** Every tag the library's resources carry, for the tag facet. */
    tags: facets.data?.tags ?? [],
    /** True when this view lists files, which is what a count line counts. */
    listing,
    /** True when a filter is narrowing the view, which is a different
     * emptiness from a folder nobody has uploaded to. */
    filtering: searching || view.tag !== "",
  };
}

/**
 * pageOf is what a view contributes from the listing: its rows, its total, and
 * whether more can be asked for.
 *
 * A view that lists no files contributes none of it, whatever the query holds.
 * The query is disabled there, so what it holds is whatever the last listing
 * left in the cache -- and a Load-more control over that is a control over rows
 * nobody is looking at (#1555).
 */
function pageOf(query: InfiniteResult<Resource>, listing: boolean) {
  return {
    resources: listing ? (query.data?.data ?? []) : [],
    total: listing ? (query.data?.total ?? 0) : 0,
    hasNextPage: listing && query.hasNextPage,
    isFetchingNextPage: listing && query.isFetchingNextPage,
    fetchNextPage: query.fetchNextPage,
  };
}

/**
 * viewShape is what kind of view an address names, which is what decides
 * whether a listing runs at all.
 *
 * A library's ROOT lists no files: every resource is filed under a folder, so
 * the root is the folder list and the recently-updated section above it. The
 * listing used to run there anyway -- to derive the tree from rows it then
 * never displayed -- which is what put a Load-more control and a
 * "showing 100 of 138" line over content that was nowhere on screen (#1555).
 *
 * Three things override that, and they are the same thing said three ways: each
 * asks a question about the library rather than about a level of it, so each
 * replaces the tree with a flat list of what it found.
 *
 *   - a search, whose hits ARE the view;
 *   - a tag, which reaches files in any folder;
 *   - an ordering other than the default, which is a curator asking the whole
 *     library "what has nothing read it", and means nothing applied to a list
 *     of folder names.
 *
 * Each of the last two was a control on screen with no listing behind it until
 * it was added here: choosing it at a root changed nothing at all.
 */
function viewShape(view: LibraryView): { searching: boolean; flat: boolean; listing: boolean } {
  const searching = view.q !== "";
  const atRoot = view.path === "";
  const asksTheLibrary = view.tag !== "" || view.sort !== "updated";
  const flat = searching || (atRoot && asksTheLibrary);
  return { searching, flat, listing: flat || !atRoot };
}

/**
 * listingFor is the request one view makes.
 *
 * The folder narrows the listing; a search replaces it, because a hit elsewhere
 * in the library is the point of searching from inside a folder.
 */
function listingFor(view: LibraryView, activeTab: string, searching: boolean) {
  return {
    path: searching ? undefined : view.path || undefined,
    tag: view.tag || undefined,
    q: view.q || undefined,
    sort: view.sort,
    ...scopeParams(activeTab),
  };
}

/** useDebounced settles a value that changes per keystroke. */
function useDebounced(value: string, delay = 300): string {
  const [settled, setSettled] = useState(value);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    const timer = setTimeout(() => setSettled(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return settled;
}
