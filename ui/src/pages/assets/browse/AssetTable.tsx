import { Eye, FolderOpen } from "lucide-react";
import type { Asset, ShareSummary } from "@/api/portal/types";
import { contentTypeIcon, ContentTypeBadge } from "@/components/ContentTypeBadge";
import { FeedbackCountBadge } from "@/components/feedback/FeedbackCountBadge";
import { SortableHead } from "@/components/patterns/SortableHead";
import { ShareIndicators } from "@/components/ShareIndicators";
import { SharePermissionBadge } from "@/components/SharePermissionBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatBytes } from "@/lib/format";
import { markdownToPlainText } from "@/lib/markdownText";
import { dateColumnFor, type AssetSortKey, type ListSort } from "@/components/listSort";
import type { DisplayAsset } from "./types";

/** The Assets list as a dense table. */
export function AssetTable({
  items,
  shareSummaries,
  threadCounts,
  sort,
  onSort,
  onNavigate,
  onPreview,
}: {
  items: DisplayAsset[];
  shareSummaries?: Record<string, ShareSummary>;
  threadCounts?: Record<string, number>;
  sort: ListSort<AssetSortKey>;
  /** Omitted when the rows are ranked rather than sorted, which makes the
   *  headers inert rather than letting them claim an ordering. */
  onSort?: (key: AssetSortKey) => void;
  onNavigate: (path: string) => void;
  onPreview: (asset: Asset) => void;
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
              className="w-[28%]"
            />
            <TableHead className="w-[10%] text-muted-foreground">Type</TableHead>
            <TableHead className="w-[15%] text-muted-foreground">Tags</TableHead>
            <TableHead className="w-[15%] text-muted-foreground">Collections</TableHead>
            <SortableHead
              label="Size"
              sortKey="size_bytes"
              sortBy={sort.key}
              sortDir={sort.dir}
              onSort={onSort}
              className="w-[8%] text-right"
            />
            <TableHead className="w-[8%] text-center text-muted-foreground">Shared</TableHead>
            <SortableHead
              label={dateKey === "created_at" ? "Created" : "Updated"}
              sortKey={dateKey}
              sortBy={sort.key}
              sortDir={sort.dir}
              onSort={onSort}
              className="w-[10%]"
            />
            <TableHead className="w-[6%]" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map(({ asset, share }) => (
            <TableRow
              key={asset.id}
              onClick={() => onNavigate(`/assets/${asset.id}`)}
              className="cursor-pointer"
            >
              <NameCell asset={asset} share={share} threadCount={threadCounts?.[asset.id]} />
              <TableCell>
                <ContentTypeBadge contentType={asset.content_type} />
              </TableCell>
              <TagsCell tags={asset.tags ?? []} />
              <CollectionsCell asset={asset} onNavigate={onNavigate} />
              <TableCell className="text-right text-muted-foreground">
                {formatBytes(asset.size_bytes)}
              </TableCell>
              <TableCell>
                {share ? (
                  <div className="flex justify-center">
                    <SharePermissionBadge permission={share.permission} />
                  </div>
                ) : (
                  <ShareIndicators summary={shareSummaries?.[asset.id]} className="justify-center gap-1.5" />
                )}
              </TableCell>
              <TableCell className="text-muted-foreground">
                {new Date(share ? share.shared_at : asset[dateKey]).toLocaleDateString()}
              </TableCell>
              <TableCell>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  title="Quick preview"
                  onClick={(e) => {
                    e.stopPropagation();
                    onPreview(asset);
                  }}
                >
                  <Eye />
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}

// ui/table sets whitespace-nowrap on every cell; the prose columns opt back in
// to wrapping so the trailing preview column is not pushed behind a scroll.
function NameCell({
  asset,
  share,
  threadCount,
}: {
  asset: Asset;
  share?: DisplayAsset["share"];
  threadCount?: number;
}) {
  const Icon = contentTypeIcon(asset.content_type);
  const secondary = share ? `Shared by ${share.shared_by}` : asset.description;
  return (
    <TableCell className="max-w-0 whitespace-normal">
      <div className="flex items-center gap-2">
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <span className="block truncate font-medium">{asset.name}</span>
          {secondary && (
            <span className="block truncate text-xs text-muted-foreground">
              {share ? secondary : markdownToPlainText(secondary)}
            </span>
          )}
        </div>
        <FeedbackCountBadge count={threadCount} />
      </div>
    </TableCell>
  );
}

function TagsCell({ tags }: { tags: string[] }) {
  return (
    <TableCell className="max-w-0 whitespace-normal">
      <div className="flex flex-wrap gap-1">
        {tags.slice(0, 3).map((t) => (
          <Badge key={t} variant="muted" className="max-w-[100px] px-1.5">
            <span className="truncate">{t}</span>
          </Badge>
        ))}
        {tags.length > 3 && <span className="text-xs text-muted-foreground">+{tags.length - 3}</span>}
      </div>
    </TableCell>
  );
}

function CollectionsCell({
  asset,
  onNavigate,
}: {
  asset: Asset;
  onNavigate: (path: string) => void;
}) {
  const collections = asset.collections ?? [];
  return (
    <TableCell className="max-w-0 whitespace-normal">
      <div className="flex flex-wrap gap-1">
        {collections.slice(0, 2).map((c) => (
          <Badge
            key={c.id}
            variant="info"
            role="button"
            tabIndex={0}
            className="max-w-[100px] cursor-pointer px-1.5"
            onClick={(e) => {
              e.stopPropagation();
              onNavigate(`/collections/${c.id}`);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.stopPropagation();
                onNavigate(`/collections/${c.id}`);
              }
            }}
          >
            <FolderOpen />
            <span className="truncate">{c.name}</span>
          </Badge>
        ))}
        {collections.length > 2 && (
          <span className="text-xs text-muted-foreground">+{collections.length - 2}</span>
        )}
      </div>
    </TableCell>
  );
}
