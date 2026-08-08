import { Plus } from "lucide-react";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { SearchInput } from "@/components/patterns/SearchInput";
import { Button } from "@/components/ui/button";
import { KnowledgeGraphView } from "../graph/KnowledgeGraphView";
import { visibleFacetTags } from "../tagFacet";
import { PageResults, TagFacet } from "./listSections";
import { useKnowledgePageBrowse } from "./useKnowledgePageBrowse";
import { ViewToggle } from "./ViewToggle";

export function KnowledgePageList({
  canEdit,
  onOpen,
  onCreate,
  onNavigate,
}: {
  canEdit: boolean;
  onOpen: (id: string) => void;
  onCreate: () => void;
  // Navigate to an in-app path, for graph nodes that are not knowledge pages.
  onNavigate?: (path: string) => void;
}) {
  const browse = useKnowledgePageBrowse();
  const { cards, searching, tag } = browse;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <CorpusCount total={browse.total} tag={searching ? "" : tag} />
        <div className="flex items-center gap-2">
          <ViewToggle value={browse.view} onChange={browse.setView} />
          {canEdit && (
            <Button size="sm" onClick={onCreate}>
              <Plus /> New page
            </Button>
          )}
        </div>
      </div>

      <SearchInput
        value={browse.query}
        onChange={(e) => browse.setQuery(e.target.value)}
        placeholder={cards ? "Search knowledge by content..." : "Find nodes in the graph..."}
      />

      {/* The cards view replaces browse with ranked results while searching, so
          the facet is hidden there; the graph keeps it, because there the query
          focuses nodes inside the tag-filtered corpus rather than replacing it
          (`searching` is false in the graph for that reason). */}
      {!searching && browse.tagCounts.length > 0 && (
        <TagFacet
          tagCounts={browse.tagCounts}
          visibleTags={visibleFacetTags(browse.tagCounts, tag, browse.tagsExpanded)}
          tag={tag}
          onSelect={browse.setTag}
          expanded={browse.tagsExpanded}
          onToggleExpanded={browse.toggleTagsExpanded}
        />
      )}

      {cards ? (
        <PageResults
          isError={browse.isError}
          loading={browse.loading}
          pages={browse.pages}
          searching={searching}
          tag={tag}
          canEdit={canEdit}
          threadCounts={browse.threadCounts}
          onOpen={onOpen}
          onCreate={onCreate}
        />
      ) : (
        <KnowledgeGraphView
          tag={tag}
          query={browse.query}
          onOpenPage={onOpen}
          onNavigate={onNavigate}
        />
      )}

      {/* Browse paginates; search returns a ranked top-K with no further pages.
          Rendered outside the list conditional so it also offers "Load more"
          when a client-side tag filter empties the loaded set but more pages
          remain (InfiniteFooter renders nothing once every page is loaded). */}
      {cards && !searching && (
        <InfiniteFooter
          hasMore={browse.paging.hasMore}
          isLoadingMore={browse.paging.isLoadingMore}
          onLoadMore={browse.paging.loadMore}
        />
      )}
    </div>
  );
}

/** CorpusCount states how much of the knowledgebase this listing covers. */
function CorpusCount({ total, tag }: { total: number; tag: string }) {
  return (
    <p className="text-sm text-muted-foreground">
      <span className="font-semibold text-foreground">{total}</span>{" "}
      {total === 1 ? "knowledge page" : "knowledge pages"}
      {tag && (
        <>
          {" "}
          tagged <span className="font-medium text-foreground">{tag}</span>
        </>
      )}
    </p>
  );
}
