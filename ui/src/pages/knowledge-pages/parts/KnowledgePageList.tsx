import { useEffect, useMemo, useState } from "react";
import { Search, Plus } from "lucide-react";
import {
  useInfiniteKnowledgePages,
  useSearchKnowledgePages,
  useThreadCounts,
  MIN_SEARCH_LEN,
} from "@/api/portal/hooks";
import type { KnowledgePage } from "@/api/portal/types";
import { FilterChip } from "@/components/FilterChip";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { useDebounced } from "@/lib/useDebounced";
import { visibleFacetTags } from "../tagFacet";
import { PageCard } from "./PageCard";

export function KnowledgePageList({ canEdit, onOpen, onCreate }: { canEdit: boolean; onOpen: (id: string) => void; onCreate: () => void }) {
  const [query, setQuery] = useState("");
  const [tag, setTag] = useState("");
  // Whether the tag facet shows every tag or just the top TAG_FACET_LIMIT.
  const [tagsExpanded, setTagsExpanded] = useState(false);
  // Debounce the input and require a minimum length before searching, so the
  // content search issues one request after the user pauses rather than one per
  // keystroke. The hook enforces the same floor as a backstop.
  const debouncedQuery = useDebounced(query, 250);
  const trimmed = debouncedQuery.trim();
  const searching = trimmed.length >= MIN_SEARCH_LEN;
  // Searching hides the facet; collapse it so returning to browse starts from the
  // compact top-N view rather than a stale expansion from before the search.
  useEffect(() => {
    if (searching) setTagsExpanded(false);
  }, [searching]);
  // Browse accumulates pages (#972). The first page loads up to 100 pages (the
  // store's honored cap), so for any knowledgebase up to that size the tag facet
  // below is complete on first render; beyond it the facet widens as more pages
  // load, an improvement over the previous single fixed request that the store
  // silently capped at 20.
  const list = useInfiniteKnowledgePages();
  const search = useSearchKnowledgePages(trimmed, { limit: 25 });

  const allPages = useMemo(() => list.data?.data ?? [], [list.data]);
  const total = list.data?.total ?? allPages.length;

  // Tag facet (tag -> count), most-used first, derived from the loaded pages.
  const tagCounts = useMemo(() => {
    const m = new Map<string, number>();
    for (const p of allPages) for (const t of p.tags) m.set(t, (m.get(t) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  }, [allPages]);

  // The capped facet chips, and how many tags that leaves hidden. The reveal
  // control only appears when something is actually hidden, so it is never a
  // dead button (e.g. when a selected over-limit tag is already pulled in).
  const visibleTags = visibleFacetTags(tagCounts, tag, tagsExpanded);
  const tagsHidden = tagCounts.length - visibleTags.length;

  // Browse list: filter by the selected tag, newest first.
  const browsePages = useMemo(() => {
    const filtered = tag ? allPages.filter((p) => p.tags.includes(tag)) : allPages;
    return [...filtered].sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  }, [allPages, tag]);

  const pages: KnowledgePage[] = searching
    ? (search.data ?? []).map((s) => s.page)
    : browsePages;
  const loading = searching ? search.isLoading : list.isLoading;

  // Open-feedback-thread counts for the visible pages, so each card can badge
  // pages that have feedback awaiting attention.
  const pageIds = useMemo(() => pages.map((p) => p.id), [pages]);
  const threadCounts = useThreadCounts("knowledge_page", pageIds);

  return (
    <div className="space-y-4">
      {/* Count + create */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          <span className="font-semibold text-foreground">{total}</span>{" "}
          {total === 1 ? "knowledge page" : "knowledge pages"}
          {!searching && tag && (
            <>
              {" "}
              tagged <span className="font-medium text-foreground">{tag}</span>
            </>
          )}
        </p>
        {canEdit && (
          <button
            onClick={onCreate}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90"
          >
            <Plus className="h-4 w-4" /> New page
          </button>
        )}
      </div>

      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search knowledge by content..."
          className="w-full rounded-md border border-border bg-background py-2 pl-9 pr-3 text-sm outline-none focus:ring-2 focus:ring-primary/40"
        />
      </div>

      {/* Tag browse (browse mode only). Cap the facet at TAG_FACET_LIMIT chips
          with a reveal for the rest, so a large tag set does not push the page
          list off-screen (#707). */}
      {!searching && tagCounts.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <FilterChip label="All" active={tag === ""} onClick={() => setTag("")} />
          {visibleTags.map(([t, c]) => (
            <FilterChip
              key={t}
              label={t}
              count={c}
              active={tag === t}
              onClick={() => setTag(tag === t ? "" : t)}
            />
          ))}
          {(tagsExpanded || tagsHidden > 0) && (
            <button
              type="button"
              onClick={() => setTagsExpanded((v) => !v)}
              className="rounded-full border border-dashed border-border px-2.5 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted"
            >
              {tagsExpanded ? "Show fewer" : `Show all (${tagCounts.length})`}
            </button>
          )}
        </div>
      )}

      {(searching ? search.isError : list.isError) ? (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Failed to load knowledge pages. Please try again.
        </p>
      ) : loading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : pages.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border p-10 text-center text-sm text-muted-foreground">
          {searching
            ? "No knowledge pages match your search."
            : tag
              ? `No pages tagged "${tag}".`
              : "No knowledge pages yet."}
          {canEdit && !searching && !tag && (
            <div className="mt-3">
              <button onClick={onCreate} className="text-primary hover:underline">
                Create the first page
              </button>
            </div>
          )}
        </div>
      ) : (
        <>
          {!searching && (
            <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {tag ? `Tagged ${tag}` : "Recently updated"}
            </p>
          )}
          <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {pages.map((p) => (
              <li key={p.id}>
                <PageCard
                  page={p}
                  openThreads={threadCounts.data?.[p.id] ?? 0}
                  onOpen={onOpen}
                />
              </li>
            ))}
          </ul>
        </>
      )}

      {/* Browse paginates; search returns a ranked top-K with no further pages.
          Rendered outside the list conditional so it also offers "Load more"
          when a client-side tag filter empties the loaded set but more pages
          remain (InfiniteFooter renders nothing once every page is loaded). */}
      {!searching && (
        <InfiniteFooter
          hasMore={list.hasNextPage}
          isLoadingMore={list.isFetchingNextPage}
          onLoadMore={list.fetchNextPage}
        />
      )}
    </div>
  );
}
