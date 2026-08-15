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
// query is in flight, the ranked flat table in search mode, or the browsed
// bucket grouped by collection. Both buckets group the same way, so a reader
// moving between the tabs reads one shape. Kept apart from the page so the page
// holds state and this holds the choice.
export function PromptResults({
  loading,
  searching,
  rows,
  groups,
  emptyMessage,
  emptyHint,
  tableProps,
}: {
  loading: boolean;
  searching: boolean;
  // The flat list: search results, which keep their server rank order.
  rows: Row[];
  // The browsed bucket, one entry per collection.
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

  if (searching) {
    if (rows.length === 0) return empty;
    return <PromptListTable rows={rows} showCollection sortable={false} {...tableProps} />;
  }

  if (groups.length === 0) return empty;

  return (
    <div className="space-y-4">
      {groups.map((group) => (
        <div key={group.collection?.id ?? "uncollected"} className="space-y-1.5">
          <div className="px-1">
            <div className="flex items-baseline gap-2">
              <h3 className="flex items-center gap-1.5 text-sm font-semibold">
                <FolderOpen className="size-3.5 text-muted-foreground" />
                {group.collection?.name ?? "General"}
              </h3>
              <span className="text-xs text-muted-foreground/70">({group.rows.length})</span>
            </div>
            {group.collection?.description && (
              // The description gets its own line under the name, indented to
              // the name's text so the folder icon leads the group alone. It
              // wraps rather than truncating: a collection's stated purpose is
              // what tells a reader which group a prompt belongs in.
              <p className="mt-0.5 pl-5 text-xs text-muted-foreground">
                {markdownToPlainText(group.collection.description)}
              </p>
            )}
          </div>
          <PromptListTable rows={group.rows} showCollection={false} sortable {...tableProps} />
        </div>
      ))}
    </div>
  );
}
