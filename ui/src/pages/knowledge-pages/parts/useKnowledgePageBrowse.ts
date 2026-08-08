import { useEffect, useMemo, useState } from "react";
import {
  useInfiniteKnowledgePages,
  useSearchKnowledgePages,
  useThreadCounts,
  MIN_SEARCH_LEN,
} from "@/api/portal/hooks";
import type { KnowledgePage } from "@/api/portal/types";
import { useDebounced } from "@/lib/useDebounced";
import type { KnowledgeView } from "./ViewToggle";

/**
 * useKnowledgePageBrowse holds how the reader has narrowed the knowledge corpus
 * — the layout, the free-text query, the tag — and resolves it to the pages on
 * screen. It is separate from the rendering because both layouts read the same
 * narrowing: switching between cards and graph must not reset it.
 */
export function useKnowledgePageBrowse() {
  const [query, setQuery] = useState("");
  const [tag, setTag] = useState("");
  // Cards or graph. The search and tag state live above this choice, so
  // switching layouts keeps the reader's current narrowing of the corpus.
  const [view, setView] = useState<KnowledgeView>("cards");
  // Whether the tag facet shows every tag or just the top TAG_FACET_LIMIT.
  const [tagsExpanded, setTagsExpanded] = useState(false);
  // Debounce the input and require a minimum length before searching, so the
  // content search issues one request after the user pauses rather than one per
  // keystroke. The hook enforces the same floor as a backstop.
  const trimmed = useDebounced(query, 250).trim();
  const cards = view === "cards";
  // "Searching" is a cards-view mode: it swaps browse for ranked results. In the
  // graph the same box focuses nodes client-side, so the content search must not
  // run there — it would issue a request per pause whose result nothing renders.
  const searching = cards && trimmed.length >= MIN_SEARCH_LEN;
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
  const search = useSearchKnowledgePages(searching ? trimmed : "", { limit: 25 });

  const allPages = useMemo(() => list.data?.data ?? [], [list.data]);

  // Tag facet (tag -> count), most-used first, derived from the loaded pages.
  const tagCounts = useMemo(() => {
    const m = new Map<string, number>();
    for (const p of allPages) for (const t of p.tags) m.set(t, (m.get(t) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  }, [allPages]);

  // Browse list: filter by the selected tag, newest first.
  const browsePages = useMemo(() => {
    const filtered = tag ? allPages.filter((p) => p.tags.includes(tag)) : allPages;
    return [...filtered].sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  }, [allPages, tag]);

  const pages: KnowledgePage[] = searching
    ? (search.data ?? []).map((s) => s.page)
    : browsePages;

  // Open-feedback-thread counts for the visible cards, so each card can badge
  // pages that have feedback awaiting attention. The graph draws no cards, so it
  // asks for none.
  const pageIds = useMemo(() => (cards ? pages.map((p) => p.id) : []), [cards, pages]);
  const threadCounts = useThreadCounts("knowledge_page", pageIds);

  const active = searching ? search : list;
  return {
    query,
    setQuery,
    tag,
    setTag,
    view,
    setView,
    cards,
    searching,
    tagsExpanded,
    toggleTagsExpanded: () => setTagsExpanded((v) => !v),
    tagCounts,
    pages,
    /** Every page the browse read knows of, which the tag facet is derived from. */
    total: list.data?.total ?? allPages.length,
    loading: active.isLoading,
    isError: active.isError,
    threadCounts: threadCounts.data ?? {},
    /** The browse read's paging, which only the cards layout pages through. */
    paging: {
      hasMore: list.hasNextPage,
      isLoadingMore: list.isFetchingNextPage,
      // Called with the click event, which is not a fetchNextPage option.
      loadMore: () => void list.fetchNextPage(),
    },
  };
}
