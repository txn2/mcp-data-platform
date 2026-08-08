import { FileText, SearchX } from "lucide-react";
import type { KnowledgePage } from "@/api/portal/types";
import { FilterChip } from "@/components/FilterChip";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { PageCard } from "./PageCard";

/**
 * TagFacet is the tag browse for the card list, capped at the top tags with a
 * reveal for the rest so a large tag set cannot push the pages off-screen
 * (#707). The reveal appears only when something is actually hidden, so it is
 * never a dead control.
 */
export function TagFacet({
  tagCounts,
  visibleTags,
  tag,
  onSelect,
  expanded,
  onToggleExpanded,
}: {
  /** Every tag in the loaded corpus with its count, most-used first. */
  tagCounts: [string, number][];
  /** The chips actually drawn, which is tagCounts capped unless expanded. */
  visibleTags: [string, number][];
  tag: string;
  onSelect: (tag: string) => void;
  expanded: boolean;
  onToggleExpanded: () => void;
}) {
  const hidden = tagCounts.length - visibleTags.length;
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <FilterChip label="All" active={tag === ""} onClick={() => onSelect("")} />
      {visibleTags.map(([t, c]) => (
        <FilterChip
          key={t}
          label={t}
          count={c}
          active={tag === t}
          onClick={() => onSelect(tag === t ? "" : t)}
        />
      ))}
      {(expanded || hidden > 0) && (
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="rounded-full text-muted-foreground"
          onClick={onToggleExpanded}
        >
          {expanded ? "Show fewer" : `Show all (${tagCounts.length})`}
        </Button>
      )}
    </div>
  );
}

/**
 * PageResults renders the card list and the four states it has before it has
 * cards: failed, still loading, matched nothing, and genuinely empty. The last
 * is where someone decides whether their reference file becomes a page (#1015),
 * so it says where such a file belongs instead.
 */
export function PageResults({
  isError,
  loading,
  pages,
  searching,
  tag,
  canEdit,
  threadCounts,
  onOpen,
  onCreate,
}: {
  isError: boolean;
  loading: boolean;
  pages: KnowledgePage[];
  /** Whether the list is showing ranked search results rather than browse. */
  searching: boolean;
  tag: string;
  canEdit: boolean;
  /** Open-feedback-thread count per page id, for the cards that have any. */
  threadCounts: Record<string, number>;
  onOpen: (id: string) => void;
  onCreate: () => void;
}) {
  if (isError) {
    return (
      <Alert variant="destructive">
        <AlertDescription>
          Failed to load knowledge pages. Please try again.
        </AlertDescription>
      </Alert>
    );
  }
  if (loading) return <p className="text-sm text-muted-foreground">Loading...</p>;
  if (pages.length === 0) {
    return (
      <EmptyResults
        searching={searching}
        tag={tag}
        canEdit={canEdit}
        onCreate={onCreate}
      />
    );
  }
  return (
    <>
      {!searching && (
        <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {tag ? `Tagged ${tag}` : "Recently updated"}
        </p>
      )}
      <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {pages.map((p) => (
          <li key={p.id}>
            <PageCard page={p} openThreads={threadCounts[p.id] ?? 0} onOpen={onOpen} />
          </li>
        ))}
      </ul>
    </>
  );
}

/** EmptyResults says why the list is empty, in the reader's terms. */
function EmptyResults({
  searching,
  tag,
  canEdit,
  onCreate,
}: {
  searching: boolean;
  tag: string;
  canEdit: boolean;
  onCreate: () => void;
}) {
  if (searching) {
    return (
      <EmptyState icon={SearchX}>No knowledge pages match your search.</EmptyState>
    );
  }
  if (tag) return <EmptyState icon={SearchX}>No pages tagged &quot;{tag}&quot;.</EmptyState>;
  return (
    <EmptyState
      icon={FileText}
      action={
        canEdit && (
          <Button variant="outline" size="sm" onClick={onCreate}>
            Create the first page
          </Button>
        )
      }
    >
      No knowledge pages yet.
      <p className="mt-2 text-xs">
        Knowledge pages are curated facts to search and synthesize. A file you wrote and want
        used as-is belongs in Resources.
      </p>
    </EmptyState>
  );
}
