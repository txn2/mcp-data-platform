import { FolderOpen } from "lucide-react";
import type { ShareSummary } from "@/api/portal/types";
import { FeedbackCountBadge } from "@/components/feedback/FeedbackCountBadge";
import { SortableHead } from "@/components/patterns/SortableHead";
import { ShareIndicators } from "@/components/ShareIndicators";
import { SharePermissionBadge } from "@/components/SharePermissionBadge";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { markdownToPlainText } from "@/lib/markdownText";
import { dateColumnFor, type CollectionSortKey, type ListSort } from "@/components/listSort";
import type { DisplayCollection } from "./types";

/** The Collections list as a dense table. */
export function CollectionTable({
  items,
  shareSummaries,
  threadCounts,
  sort,
  onSort,
  onNavigate,
}: {
  items: DisplayCollection[];
  shareSummaries?: Record<string, ShareSummary>;
  threadCounts?: Record<string, number>;
  sort: ListSort<CollectionSortKey>;
  /** Omitted when the rows are ranked rather than sorted, which makes the
   *  headers inert rather than letting them claim an ordering. */
  onSort?: (key: CollectionSortKey) => void;
  onNavigate: (path: string) => void;
}) {
  const dateKey = dateColumnFor(sort.key);
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <Table className="table-fixed">
        <TableHeader>
          <TableRow className="bg-muted/50">
            <SortableHead
              label="Name"
              sortKey="name"
              sortBy={sort.key}
              sortDir={sort.dir}
              onSort={onSort}
              className="w-[35%]"
            />
            <TableHead className="w-[30%] text-muted-foreground">Tags</TableHead>
            <TableHead className="w-[8%] text-center text-muted-foreground">Shared</TableHead>
            <SortableHead
              label={dateKey === "created_at" ? "Created" : "Updated"}
              sortKey={dateKey}
              sortBy={sort.key}
              sortDir={sort.dir}
              onSort={onSort}
              className="w-[12%]"
            />
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map(({ collection: coll, share }) => {
            const tags = coll.asset_tags ?? [];
            const secondary = share ? `Shared by ${share.shared_by}` : coll.description;
            return (
              <TableRow
                key={coll.id}
                onClick={() => onNavigate(`/collections/${coll.id}`)}
                className="cursor-pointer"
              >
                {/* ui/table sets whitespace-nowrap on every cell; the prose
                    columns opt back in so nothing is pushed behind a scroll. */}
                <TableCell className="max-w-0 whitespace-normal">
                  <div className="flex items-center gap-2">
                    <FolderOpen className="size-4 shrink-0 text-muted-foreground" />
                    <div className="min-w-0 flex-1">
                      <span className="block truncate font-medium">{coll.name}</span>
                      {secondary && (
                        <span className="block truncate text-xs text-muted-foreground">
                          {share ? secondary : markdownToPlainText(secondary)}
                        </span>
                      )}
                    </div>
                    <FeedbackCountBadge count={threadCounts?.[coll.id]} />
                  </div>
                </TableCell>
                <TableCell className="max-w-0 whitespace-normal">
                  <div className="flex flex-wrap gap-1">
                    {tags.slice(0, 4).map((t) => (
                      <Badge key={t} variant="muted" className="max-w-[100px] px-1.5">
                        <span className="truncate">{t}</span>
                      </Badge>
                    ))}
                    {tags.length > 4 && (
                      <span className="text-xs text-muted-foreground">+{tags.length - 4}</span>
                    )}
                  </div>
                </TableCell>
                <TableCell>
                  {share ? (
                    <div className="flex justify-center">
                      <SharePermissionBadge permission={share.permission} />
                    </div>
                  ) : (
                    <ShareIndicators
                      summary={shareSummaries?.[coll.id]}
                      className="justify-center gap-1.5"
                    />
                  )}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {new Date(share ? share.shared_at : coll[dateKey]).toLocaleDateString()}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </Card>
  );
}
