import { useState, useEffect, useCallback, useMemo } from "react";
import {
  useMyPrompts,
  useSearchMyPrompts,
  useSharedPrompts,
  usePromptUsage,
  usePromptCollections,
} from "@/api/portal/hooks";
import type { Prompt, PromptCollection } from "@/api/admin/types";
import { CollectionsManagerDialog } from "./CollectionsManagerDialog";
import { PromptCreateForm } from "./PromptCreateForm";
import { PromptFacetsBar } from "./PromptFacetsBar";
import { LibraryToolbar } from "./library/LibraryToolbar";
import { PromptResults } from "./library/PromptResults";
import type { LibraryGroup, Tab } from "./library/types";
import type { UsageFacet } from "./promptUsage";
import {
  allFacets,
  facetsActive,
  matchesFacets,
  sortRows,
  type Facets,
  type Row,
  type SortDir,
  type SortKey,
} from "./promptList";

interface Props {
  onNavigate: (path: string) => void;
}

export function MyPromptsPage({ onNavigate }: Props) {
  const [tab, setTab] = useState<Tab>("mine");
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [creating, setCreating] = useState(false);
  const [managingCollections, setManagingCollections] = useState(false);
  const [sortBy, setSortBy] = useState<SortKey>("name");
  const [sortDir, setSortDir] = useState<SortDir>("asc");
  const [facets, setFacets] = useState<Facets>(allFacets);

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(timer);
  }, [search]);

  const { data, isLoading } = useMyPrompts();
  const { data: sharedPrompts = [], isLoading: sharedLoading } = useSharedPrompts();
  // usageReady distinguishes "usage unknown" (query pending or failed) from
  // "absent from the map = never run": rows show a dash, never a false
  // "inactive" claim, until the rollup actually arrives.
  const { data: usageMap, isSuccess: usageReady } = usePromptUsage();
  const { data: collectionData } = usePromptCollections();
  const searching = debouncedSearch.trim().length > 0;
  const searchResults = useSearchMyPrompts(debouncedSearch);

  const collections = useMemo(() => collectionData?.data ?? [], [collectionData]);
  const collectionById = useMemo(() => {
    const m = new Map<string, PromptCollection>();
    for (const c of collections) m.set(c.id, c);
    return m;
  }, [collections]);

  // My Prompts: everything the caller owns plus shared-with-me, attributed.
  const myRows = useMemo<Row[]>(() => {
    const own = (data?.personal ?? []).map((p) => ({ prompt: p }));
    const shared = sharedPrompts.map((s) => ({ prompt: s.prompt, sharedBy: s.shared_by }));
    return [...own, ...shared];
  }, [data, sharedPrompts]);

  // Library: the approved shared prompts visible to the caller.
  const libraryRows = useMemo<Row[]>(
    () => (data?.available ?? []).map((p) => ({ prompt: p })),
    [data],
  );

  const rows = tab === "mine" ? myRows : libraryRows;
  const isMineTab = tab === "mine";

  // Facet vocabularies come from the active bucket's rows.
  const tagOptions = useMemo(() => {
    const tags = new Set<string>();
    for (const r of rows) for (const t of r.prompt.tags ?? []) tags.add(t);
    return [...tags].sort();
  }, [rows]);
  const ownerOptions = useMemo(() => {
    const owners = new Set<string>();
    for (const r of rows) if (r.prompt.owner_email) owners.add(r.prompt.owner_email);
    return [...owners].sort();
  }, [rows]);

  // handleSort toggles direction on the active column, or activates a new
  // column at its default direction (usage sorts default descending, most
  // active first). State updates stay outside the updater functions so
  // StrictMode's double-invocation cannot cancel the toggle.
  const handleSort = useCallback(
    (key: SortKey) => {
      if (sortBy === key) {
        setSortDir((d) => (d === "asc" ? "desc" : "asc"));
        return;
      }
      setSortBy(key);
      setSortDir(key === "name" ? "asc" : "desc");
    },
    [sortBy],
  );

  const visibleRows = useMemo(() => {
    // Without usage data the activity facet must not misclassify everything
    // as inactive, so it is suspended until the rollup arrives.
    const effective = usageReady ? facets : { ...facets, usage: "all" as UsageFacet };
    return sortRows(
      rows.filter((r) => matchesFacets(r, effective, usageMap?.[r.prompt.id])),
      sortBy,
      sortDir,
      usageMap,
    );
  }, [rows, facets, usageMap, usageReady, sortBy, sortDir]);

  // Both buckets group by collection; uncollected prompts land in the trailing
  // default group. A prompt shared from a collection the caller cannot see
  // groups as uncollected, since the group header would have no name to carry.
  const grouped = useMemo<LibraryGroup[]>(() => {
    const groups = new Map<string, Row[]>();
    for (const r of visibleRows) {
      const key = r.prompt.collection_id && collectionById.has(r.prompt.collection_id)
        ? r.prompt.collection_id
        : "";
      const list = groups.get(key) ?? [];
      list.push(r);
      groups.set(key, list);
    }
    const named = [...groups.entries()]
      .filter(([id]) => id !== "")
      .map(([id, list]) => ({ collection: collectionById.get(id), rows: list }))
      .sort((a, b) => (a.collection?.name ?? "").localeCompare(b.collection?.name ?? ""));
    const rest = groups.get("");
    if (rest) named.push({ collection: undefined, rows: rest });
    return named;
  }, [visibleRows, collectionById]);

  // Search mode: server-ranked results across personal and Library, plus
  // client-side matches from the shared-with-me list (which the server's
  // ranked search does not cover), so both buckets are searched.
  const searchRows = useMemo<Row[]>(() => {
    const ranked = (searchResults.data?.data ?? []).map((s) => ({ prompt: s.prompt }));
    const q = debouncedSearch.trim().toLowerCase();
    const seen = new Set(ranked.map((r) => r.prompt.id));
    const sharedMatches = sharedPrompts
      .filter((s) => !seen.has(s.prompt.id))
      .filter(
        (s) =>
          (s.prompt.display_name || s.prompt.name || "").toLowerCase().includes(q) ||
          (s.prompt.description || "").toLowerCase().includes(q),
      )
      .map((s) => ({ prompt: s.prompt, sharedBy: s.shared_by }));
    return [...ranked, ...sharedMatches];
  }, [searchResults.data, sharedPrompts, debouncedSearch]);

  const listLoading = searching ? searchResults.isLoading : isLoading || (isMineTab && sharedLoading);
  const filtersOn = facetsActive(facets);

  // switchTab resets the facets: their vocabularies (tags, owners) and the
  // tab-specific selects (status vs owner) belong to one bucket, so a filter
  // set on one tab must not silently narrow the other.
  function switchTab(next: Tab) {
    setTab(next);
    setFacets(allFacets);
  }

  const openPrompt = useCallback((p: Prompt) => onNavigate(`/prompts/${p.id}`), [onNavigate]);

  const tableProps = {
    sortBy,
    sortDir,
    onSort: handleSort,
    usageMap,
    usageReady,
    collectionById,
    showStatus: isMineTab,
    onOpen: openPrompt,
  };

  const emptyMessage = searching
    ? `No prompts match "${debouncedSearch.trim()}"`
    : filtersOn
      ? "No prompts match the current filters"
      : isMineTab
        ? "You don't own any prompts yet"
        : "The library is empty";

  const emptyHint = searching || filtersOn
    ? undefined
    : isMineTab
      ? "Create a prompt, or open the Library tab for your team's approved prompts."
      : "Approved team prompts appear here once promoted.";

  return (
    <div className="space-y-4">
      <LibraryToolbar
        tab={tab}
        onTabChange={switchTab}
        mineCount={myRows.length}
        libraryCount={libraryRows.length}
        search={search}
        onSearchChange={setSearch}
        onManageCollections={() => setManagingCollections(true)}
        onCreate={isMineTab ? () => setCreating(true) : undefined}
      />

      {/* Facets (browse mode; search results keep their rank order) */}
      {!searching && (
        <PromptFacetsBar
          facets={facets}
          onChange={setFacets}
          collections={collections}
          tagOptions={tagOptions}
          ownerOptions={ownerOptions}
          isMineTab={isMineTab}
        />
      )}

      {creating && (
        <PromptCreateForm
          onCreated={(p) => { setCreating(false); if (p?.id) onNavigate(`/prompts/${p.id}`); }}
          onClose={() => setCreating(false)}
        />
      )}

      {/* Relevance hint (search mode) */}
      {searching && (
        <p className="text-xs text-muted-foreground">
          Ranked by relevance to &ldquo;{debouncedSearch.trim()}&rdquo; across your prompts and the library.
        </p>
      )}

      <PromptResults
        loading={listLoading}
        searching={searching}
        rows={searchRows}
        groups={grouped}
        emptyMessage={emptyMessage}
        emptyHint={emptyHint}
        tableProps={tableProps}
      />

      {managingCollections && (
        <CollectionsManagerDialog onClose={() => setManagingCollections(false)} />
      )}
    </div>
  );
}
