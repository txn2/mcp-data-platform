import { useEffect, useState } from "react";
import { useInfiniteResources } from "@/api/resources/hooks";

export type ResourceSort = "updated" | "last_read";

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

// useResourceLibrary owns what the library is currently showing — which scope,
// which filters, which order — and the page of resources that follows from it,
// so the page component stays a rendering concern.
//
// The free-text box is debounced against the query rather than the input, so
// typing does not fire a request per keystroke while still reading live.
//
// `scopes` is the set of libraries the caller may look at; the selection is
// clamped to it (see liveScope above).
export function useResourceLibrary(admin: boolean, scopes: string[]) {
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("");
  const [sort, setSort] = useState<ResourceSort>("updated");
  const [selectedTab, setSelectedTab] = useState<string>(admin ? "all" : "user");
  const activeTab = liveScope(selectedTab, scopes);

  useEffect(() => {
    const timer = setTimeout(() => setSearch(searchInput), 300);
    return () => clearTimeout(timer);
  }, [searchInput]);

  const query = useInfiniteResources({
    category: category || undefined,
    q: search || undefined,
    sort,
    ...scopeParams(activeTab),
  });

  const resources = query.data?.data ?? [];
  const total = query.data?.total ?? 0;

  return {
    searchInput,
    setSearchInput,
    category,
    setCategory,
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
    // A narrowed view that returns nothing means the filter missed, not that
    // the scope is empty; the two need different empty states.
    filtering: search !== "" || category !== "",
  };
}
