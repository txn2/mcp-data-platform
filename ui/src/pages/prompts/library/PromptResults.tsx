import type { ComponentProps } from "react";
import { FolderOpen, MessageSquare } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import { markdownToPlainText } from "@/lib/markdownText";
import { ListSkeleton } from "../primitives";
import { PromptListTable } from "../PromptListTable";
import type { Row } from "../promptList";
import type { LibraryGroup } from "./types";

// The row-independent half of the table's props: the page owns sorting, usage,
// and navigation, and each rendering below supplies only its own rows.
type TableProps = Omit<
  ComponentProps<typeof PromptListTable>,
  "rows" | "showCollection" | "sortable"
>;

// PromptResults picks the one list the library shows: the skeleton while a
// query is in flight, the ranked flat table in search mode, the flat owned
// table on My Prompts, or the Library bucket grouped by collection. Kept apart
// from the page so the page holds state and this holds the choice.
export function PromptResults({
  loading,
  searching,
  isMineTab,
  rows,
  groups,
  emptyMessage,
  emptyHint,
  tableProps,
}: {
  loading: boolean;
  searching: boolean;
  isMineTab: boolean;
  // The flat list: search results, or the caller's own prompts.
  rows: Row[];
  // The Library bucket in browse mode, one entry per collection.
  groups: LibraryGroup[];
  emptyMessage: string;
  // Shown only when nothing is filtered out — a hint about an empty bucket is
  // wrong advice when the emptiness came from the facets.
  emptyHint?: string;
  tableProps: TableProps;
}) {
  const empty = (
    <EmptyState icon={MessageSquare}>
      <p className="font-medium">{emptyMessage}</p>
      {emptyHint && <p className="mt-1 text-xs">{emptyHint}</p>}
    </EmptyState>
  );

  if (loading) return <ListSkeleton />;

  if (searching || isMineTab) {
    if (rows.length === 0) return empty;
    return <PromptListTable rows={rows} showCollection sortable={!searching} {...tableProps} />;
  }

  if (groups.length === 0) return empty;

  return (
    <div className="space-y-4">
      {groups.map((group) => (
        <div key={group.collection?.id ?? "uncollected"} className="space-y-1.5">
          <div className="flex items-baseline gap-2 px-1">
            <h3 className="flex items-center gap-1.5 text-sm font-semibold">
              <FolderOpen className="size-3.5 text-muted-foreground" />
              {group.collection?.name ?? "General"}
            </h3>
            {group.collection?.description && (
              <span className="truncate text-xs text-muted-foreground">
                {markdownToPlainText(group.collection.description)}
              </span>
            )}
            <span className="text-xs text-muted-foreground/70">({group.rows.length})</span>
          </div>
          <PromptListTable rows={group.rows} showCollection={false} sortable {...tableProps} />
        </div>
      ))}
    </div>
  );
}
