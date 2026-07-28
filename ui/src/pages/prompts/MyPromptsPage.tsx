import { useState, useEffect, useCallback, useMemo } from "react";
import { Search, Plus, MessageSquare, FolderOpen } from "lucide-react";
import {
  useMyPrompts,
  useSearchMyPrompts,
  useSharedPrompts,
  usePromptUsage,
  usePromptCollections,
} from "@/api/portal/hooks";
import type { Prompt, PromptCollection } from "@/api/admin/types";
import { markdownToPlainText } from "@/lib/markdownText";
import { cn } from "@/lib/utils";
import { CollectionsManagerDialog } from "./CollectionsManagerDialog";
import { PromptCreateForm } from "./PromptCreateForm";
import { PromptFacetsBar } from "./PromptFacetsBar";
import { PromptListTable } from "./PromptListTable";
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

// The library's two buckets (#1010): My Prompts is the caller's personal
// prompts plus prompts shared with them (attributed); Library is the approved
// shared set visible to them, grouped by collection. Scope taxonomy stays out
// of this page.
type Tab = "mine" | "library";

interface LibraryGroup {
  collection: PromptCollection | undefined;
  rows: Row[];
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

  // My Prompts: personal plus shared-with-me, attributed.
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

  // Library groups by collection; uncollected prompts land in the trailing
  // default group. My Prompts stays flat (a collection column instead).
  const groupedLibrary = useMemo<LibraryGroup[]>(() => {
    if (isMineTab) return [];
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
  }, [isMineTab, visibleRows, collectionById]);

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
        ? "No personal prompts yet"
        : "The library is empty";

  return (
    <div className="space-y-4">
      {/* Toolbar */}
      <div className="flex items-center gap-3">
        <div className="flex rounded-md border bg-muted/50">
          <button
            onClick={() => switchTab("mine")}
            className={cn("px-3 py-1.5 text-sm font-medium rounded-md", tab === "mine" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}
          >
            My Prompts ({myRows.length})
          </button>
          <button
            onClick={() => switchTab("library")}
            className={cn("px-3 py-1.5 text-sm font-medium rounded-md", tab === "library" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}
          >
            Library ({libraryRows.length})
          </button>
        </div>
        <div className="relative max-w-md flex-1">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input type="text" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search prompts by meaning..." className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2" />
        </div>
        <button
          onClick={() => setManagingCollections(true)}
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-2 text-sm font-medium hover:bg-accent whitespace-nowrap"
        >
          <FolderOpen className="h-4 w-4" /> Collections
        </button>
        {isMineTab && (
          <button onClick={() => setCreating(true)} className="ml-auto inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 whitespace-nowrap">
            <Plus className="h-4 w-4" /> New Prompt
          </button>
        )}
      </div>

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

      {/* Lists */}
      {listLoading ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">{searching ? "Searching..." : "Loading..."}</div>
      ) : searching ? (
        searchRows.length === 0 ? (
          <EmptyState message={emptyMessage} />
        ) : (
          <PromptListTable rows={searchRows} showCollection sortable={false} {...tableProps} />
        )
      ) : isMineTab ? (
        visibleRows.length === 0 ? (
          <EmptyState message={emptyMessage} hint={!filtersOn ? "Create a prompt, or open the Library tab for your team's approved prompts." : undefined} />
        ) : (
          <PromptListTable rows={visibleRows} showCollection sortable {...tableProps} />
        )
      ) : groupedLibrary.length === 0 ? (
        <EmptyState message={emptyMessage} hint={!filtersOn ? "Approved team prompts appear here once promoted." : undefined} />
      ) : (
        <div className="space-y-4">
          {groupedLibrary.map((group) => (
            <div key={group.collection?.id ?? "uncollected"} className="space-y-1.5">
              <div className="flex items-baseline gap-2 px-1">
                <h3 className="flex items-center gap-1.5 text-sm font-semibold">
                  <FolderOpen className="h-3.5 w-3.5 text-muted-foreground" />
                  {group.collection?.name ?? "General"}
                </h3>
                {group.collection?.description && (
                  <span className="text-xs text-muted-foreground truncate">
                    {markdownToPlainText(group.collection.description)}
                  </span>
                )}
                <span className="text-xs text-muted-foreground/70">({group.rows.length})</span>
              </div>
              <PromptListTable rows={group.rows} showCollection={false} sortable {...tableProps} />
            </div>
          ))}
        </div>
      )}

      {managingCollections && (
        <CollectionsManagerDialog onClose={() => setManagingCollections(false)} />
      )}
    </div>
  );
}

function EmptyState({ message, hint }: { message: string; hint?: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
      <MessageSquare className="h-12 w-12 mb-2 opacity-30" />
      <p className="text-sm font-medium">{message}</p>
      {hint && <p className="text-xs mt-1">{hint}</p>}
    </div>
  );
}
