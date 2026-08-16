import { useState, useEffect } from "react";
import { File } from "lucide-react";
import { useInfiniteAdminAssets } from "@/api/admin/hooks";
import { AssetsTabs } from "@/components/AssetsTabs";
import { contentTypeIcon } from "@/components/ContentTypeBadge";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SearchInput } from "@/components/patterns/SearchInput";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { ShareIndicators } from "@/components/ShareIndicators";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatBytes, formatOwner } from "@/lib/format";

interface Props {
  onNavigate: (path: string) => void;
}

export function AdminAssetsPage({ onNavigate }: Props) {
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(timer);
  }, [search]);

  const { data, isLoading, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useInfiniteAdminAssets({
      search: debouncedSearch || undefined,
    });

  const assets = data?.data ?? [];

  return (
    <div className="space-y-4">
      <AssetsTabs active="assets" admin onNavigate={onNavigate} />

      <SearchInput
        className="max-w-md"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        placeholder="Search by name, description, owner, or tag..."
      />

      {isLoading ? (
        <p className="py-12 text-center text-sm text-muted-foreground">Loading...</p>
      ) : assets.length === 0 ? (
        <EmptyState icon={File}>
          <p className="font-medium">No assets found</p>
        </EmptyState>
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <Table className="table-fixed">
            <TableHeader>
              <TableRow className="bg-muted/50">
                <TableHead className="w-[40%] text-muted-foreground">Name</TableHead>
                <TableHead className="w-[20%] text-muted-foreground">Owner</TableHead>
                <TableHead className="w-[12%] text-muted-foreground">Type</TableHead>
                <TableHead className="w-[8%] text-right text-muted-foreground">Size</TableHead>
                <TableHead className="w-[8%] text-center text-muted-foreground">Shared</TableHead>
                {/* The admin list has no sort control, so it names the one
                    date it is ordered by: the platform lists every asset most
                    recently touched first. */}
                <TableHead className="w-[12%] text-muted-foreground">Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {assets.map((asset) => {
                const Icon = contentTypeIcon(asset.content_type);
                return (
                  <TableRow
                    key={asset.id}
                    onClick={() => onNavigate(`/admin/assets/${asset.id}`)}
                    className="cursor-pointer"
                  >
                    {/* ui/table sets whitespace-nowrap on every cell; the two
                        prose columns opt back in so the trailing columns are
                        not pushed behind a horizontal scroll. */}
                    <TableCell className="max-w-0 whitespace-normal">
                      <div className="flex items-center gap-2">
                        <Icon className="size-4 shrink-0 text-muted-foreground" />
                        <span className="truncate font-medium">{asset.name}</span>
                      </div>
                    </TableCell>
                    <TableCell className="max-w-0 whitespace-normal">
                      <span className="block truncate text-muted-foreground">{formatOwner(asset)}</span>
                    </TableCell>
                    <TableCell>
                      <span className="font-mono text-xs text-muted-foreground">
                        {asset.content_type}
                      </span>
                    </TableCell>
                    <TableCell className="text-right text-muted-foreground">
                      {formatBytes(asset.size_bytes)}
                    </TableCell>
                    <TableCell>
                      <ShareIndicators
                        summary={data?.share_summaries?.[asset.id]}
                        className="justify-center gap-1.5"
                      />
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {new Date(asset.updated_at).toLocaleDateString()}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
      )}

      <InfiniteFooter
        hasMore={hasNextPage}
        isLoadingMore={isFetchingNextPage}
        // Wrapped: InfiniteFooter hands its click handler the event, which
        // fetchNextPage would read as its options argument.
        onLoadMore={() => fetchNextPage()}
      />

      {data && assets.length < data.total && (
        <p className="text-center text-sm text-muted-foreground">
          Showing {assets.length} of {data.total} assets
        </p>
      )}
    </div>
  );
}
