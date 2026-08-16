import { useState, useEffect } from "react";
import { FolderOpen } from "lucide-react";
import { useInfiniteAdminCollections } from "@/api/admin/hooks";
import { AssetsTabs } from "@/components/AssetsTabs";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SearchInput } from "@/components/patterns/SearchInput";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { ShareIndicators } from "@/components/ShareIndicators";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatOwner } from "@/lib/format";
import { markdownToPlainText } from "@/lib/markdownText";

interface Props {
  onNavigate: (path: string) => void;
}

/**
 * Every asset collection on the platform, whoever owns it.
 *
 * The portal list is owner-scoped, which left a collection created by another
 * principal — an agent session running under an API-key identity, most sharply —
 * visible to nobody, while the assets it grouped stayed listed one tab over
 * (#1292). This is the collection half of the admin asset view.
 */
export function AdminCollectionsPage({ onNavigate }: Props) {
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(timer);
  }, [search]);

  const { data, isLoading, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useInfiniteAdminCollections({ search: debouncedSearch || undefined });

  const collections = data?.data ?? [];

  return (
    <div className="space-y-4">
      <AssetsTabs active="collections" admin onNavigate={onNavigate} />

      <SearchInput
        className="max-w-md"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        placeholder="Search by name, description, or owner..."
      />

      {isLoading ? (
        <p className="py-12 text-center text-sm text-muted-foreground">Loading...</p>
      ) : collections.length === 0 ? (
        <EmptyState icon={FolderOpen}>
          <p className="font-medium">No collections found</p>
        </EmptyState>
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <Table className="table-fixed">
            <TableHeader>
              <TableRow className="bg-muted/50">
                <TableHead className="w-[38%] text-muted-foreground">Name</TableHead>
                <TableHead className="w-[20%] text-muted-foreground">Owner</TableHead>
                <TableHead className="w-[22%] text-muted-foreground">Tags</TableHead>
                <TableHead className="w-[8%] text-center text-muted-foreground">Shared</TableHead>
                {/* The admin list has no sort control, so it names the one date
                    it is ordered by: most recently touched first. */}
                <TableHead className="w-[12%] text-muted-foreground">Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {collections.map((coll) => {
                const tags = coll.asset_tags ?? [];
                return (
                  <TableRow
                    key={coll.id}
                    onClick={() => onNavigate(`/admin/collections/${coll.id}`)}
                    className="cursor-pointer"
                  >
                    {/* ui/table sets whitespace-nowrap on every cell; the prose
                        columns opt back in so the trailing columns are not
                        pushed behind a horizontal scroll. */}
                    <TableCell className="max-w-0 whitespace-normal">
                      <div className="flex items-center gap-2">
                        <FolderOpen className="size-4 shrink-0 text-muted-foreground" />
                        <div className="min-w-0 flex-1">
                          <span className="block truncate font-medium">{coll.name}</span>
                          {coll.description && (
                            <span className="block truncate text-xs text-muted-foreground">
                              {markdownToPlainText(coll.description)}
                            </span>
                          )}
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="max-w-0 whitespace-normal">
                      <span className="block truncate text-muted-foreground">
                        {formatOwner(coll)}
                      </span>
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
                      <ShareIndicators
                        summary={data?.share_summaries?.[coll.id]}
                        className="justify-center gap-1.5"
                      />
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {new Date(coll.updated_at).toLocaleDateString()}
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

      {data && collections.length < data.total && (
        <p className="text-center text-sm text-muted-foreground">
          Showing {collections.length} of {data.total} collections
        </p>
      )}
    </div>
  );
}
