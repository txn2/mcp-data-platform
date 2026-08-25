import { useEffect, useRef, useState } from "react";
import { useInfiniteResources } from "@/api/resources/hooks";
import { libraryPath, readLibraryView, type ResourceSort } from "./libraryUrl";

export type { ResourceSort } from "./libraryUrl";

// scopeParams narrows a query to the library the active tab names. The admin
// "all" tab sends no scope at all, which is what makes it unfiltered.
function scopeParams(activeTab: string): Record<string, string> {
  if (activeTab === "all") return {};
  if (activeTab === "user" || activeTab === "global") return { scope: activeTab };
  return { scope: "persona", scope_id: activeTab };
}

// liveScope keeps the selection on a library that still exists. The persona
// scopes come from a live list, so the one in view can disappear under the
// reader; falling back to the first keeps the query and the tab strip agreeing
// instead of leaving the page with nothing selected and nothing shown.
function liveScope(selected: string, scopes: string[]): string {
  return scopes.includes(selected) ? selected : (scopes[0] ?? selected);
}

// A narrowed view that returns nothing means the filter missed, not that the
// scope is empty; the two need different empty states.
function narrowed(q: string, category: string, tag: string): boolean {
  return q !== "" || category !== "" || tag !== "";
}

interface LibrarySection {
  /** Where this library is mounted: "/resources" or "/admin/resources". */
  basePath: string;
  /** The shell's navigate, used to keep the address bar on the view. */
  onNavigate?: (path: string, opts?: { replace?: boolean }) => void;
}

// useResourceLibrary owns what the library is currently showing — which scope,
// which filters, which order — and the page of resources that follows from it,
// so the page component stays a rendering concern.
//
// The free-text box is debounced against the query rather than the input, so
// typing does not fire a request per keystroke while still reading live. The
// address bar is written from the debounced value for the same reason.
//
// `scopes` is the set of libraries the caller may look at; the selection is
// clamped to it (see liveScope above). The clamp applies to what is queried and
// not to what is written back to the address bar: the persona scopes arrive
// asynchronously, so a deep link naming one is briefly outside the set, and
// canonicalizing the address off the clamped value would discard the link
// before its scope had loaded.
export function useResourceLibrary(admin: boolean, scopes: string[], section: LibrarySection) {
  const defaultTab = admin ? "all" : "user";
  const { basePath, onNavigate } = section;
  const initial = useRef(readLibraryView(window.location.search, defaultTab)).current;
  const [searchInput, setSearchInput] = useState(initial.q);
  const [search, setSearch] = useState(initial.q);
  const [category, setCategory] = useState(initial.category);
  const [tag, setTag] = useState(initial.tag);
  const [sort, setSort] = useState<ResourceSort>(initial.sort);
  const [selectedTab, setSelectedTab] = useState<string>(initial.tab);
  const activeTab = liveScope(selectedTab, scopes);

  useEffect(() => {
    const timer = setTimeout(() => setSearch(searchInput), 300);
    return () => clearTimeout(timer);
  }, [searchInput]);

  // Two addresses for the same library, differing only in the search box.
  //
  // The bar is written from the debounced value, for the reason the query is:
  // a keystroke is not a view worth recording. Leaving the page pins the live
  // one, because a filter typed and immediately clicked through has not reached
  // the debounced value yet, and pinning that would drop what is on screen.
  const settled = libraryPath(
    basePath,
    { tab: selectedTab, q: search, category, tag, sort },
    defaultTab,
  );
  const live = libraryPath(
    basePath,
    { tab: selectedTab, q: searchInput, category, tag, sort },
    defaultTab,
  );

  // replace, not push: the reader is narrowing one library rather than moving
  // between pages, and a history entry per filter change would make Back walk
  // backwards through their own typing instead of returning them to where they
  // came from.
  useEffect(() => {
    onNavigate?.(settled, { replace: true });
  }, [settled, onNavigate]);

  const query = useInfiniteResources({
    category: category || undefined,
    tag: tag || undefined,
    q: search || undefined,
    sort,
    ...scopeParams(activeTab),
  });

  const resources = query.data?.data ?? [];
  const total = query.data?.total ?? 0;

  return {
    // Where this library is showing what it is showing, search box included
    // even mid-debounce. The page writes it into the entry it is leaving before
    // pushing a resource, so Back returns to this view even when the address
    // had drifted from it -- which it does whenever the shell navigates to the
    // plain library path while a filter is set, and the library, being neither
    // remounted nor re-rendered by that, never learns of it.
    address: live,
    searchInput,
    setSearchInput,
    category,
    setCategory,
    tag,
    setTag,
    sort,
    setSort,
    activeTab,
    setActiveTab: setSelectedTab,
    resources,
    total,
    isLoading: query.isLoading,
    hasNextPage: query.hasNextPage,
    isFetchingNextPage: query.isFetchingNextPage,
    fetchNextPage: query.fetchNextPage,
    filtering: narrowed(search, category, tag),
  };
}
