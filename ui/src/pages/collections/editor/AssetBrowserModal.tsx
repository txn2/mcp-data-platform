import { useState, useMemo } from "react";
import { Eye, FileText, X } from "lucide-react";
import type { Asset } from "@/api/portal/types";
import { AssetPreviewModal } from "@/components/AssetPreviewModal";
import { AuthImg } from "@/components/AuthImg";
import { ModalShell } from "@/components/ModalShell";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SortableHead } from "@/components/patterns/SortableHead";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

type SortKey = "name" | "created_at";

/** Asset browser modal for adding assets to a section. */
export function AssetBrowserModal({
  assets,
  onAdd,
  onClose,
}: {
  assets: Asset[];
  onAdd: (asset: Asset) => void;
  onClose: () => void;
}) {
  const [search, setSearch] = useState("");
  const [sortBy, setSortBy] = useState<SortKey>("name");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");
  const [previewing, setPreviewing] = useState<Asset | null>(null);

  function toggleSort(col: SortKey) {
    if (sortBy === col) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(col);
      // Names read best from A; timestamps read best from the newest.
      setSortDir(col === "name" ? "asc" : "desc");
    }
  }

  const filtered = useMemo(() => sortAssets(filterAssets(assets, search), sortBy, sortDir), [
    assets,
    search,
    sortBy,
    sortDir,
  ]);

  return (
    <>
      <ModalShell
        onClose={onClose}
        width="max-w-2xl"
        label="Add Assets"
        bodyClass="px-4 pt-2 pb-4"
        header={
          // The search box stays with the title rather than scrolling away
          // with the rows it filters.
          <div className="space-y-3 border-b p-4">
            <div className="flex items-center gap-3">
              <h3 className="flex-1 text-sm font-semibold">Add Assets</h3>
              <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
                <X />
              </Button>
            </div>
            <SearchInput
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search by name, description, or tag..."
              autoFocus
            />
          </div>
        }
      >
        <Table>
          <TableHeader className="sticky top-0 bg-card">
            <TableRow>
              <TableHead className="w-10" />
              <SortableHead
                label="Name"
                sortKey="name"
                sortBy={sortBy}
                sortDir={sortDir}
                onSort={toggleSort}
              />
              <TableHead className="w-[20%] text-muted-foreground">Type</TableHead>
              <SortableHead
                label="Created"
                sortKey="created_at"
                sortBy={sortBy}
                sortDir={sortDir}
                onSort={toggleSort}
                className="w-[25%]"
              />
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((a) => (
              <TableRow key={a.id}>
                <TableCell>
                  {a.thumbnail_s3_key ? (
                    <AuthImg
                      src={`/api/v1/portal/assets/${a.id}/thumbnail`}
                      alt=""
                      className="h-6 w-8 rounded object-cover"
                    />
                  ) : (
                    <div className="flex h-6 w-8 items-center justify-center rounded bg-muted">
                      <FileText className="size-3 text-muted-foreground/50" />
                    </div>
                  )}
                </TableCell>
                <TableCell className="max-w-0 whitespace-normal">
                  <span className="block truncate font-medium">{a.name}</span>
                  {(a.tags ?? []).length > 0 && (
                    <div className="mt-0.5 flex gap-1">
                      {(a.tags ?? []).slice(0, 3).map((t) => (
                        <Badge key={t} variant="muted" className="px-1">
                          {t}
                        </Badge>
                      ))}
                    </div>
                  )}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{a.content_type}</TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {new Date(a.created_at).toLocaleDateString()}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-1.5">
                    <Button
                      variant="secondary"
                      size="icon-xs"
                      onClick={() => setPreviewing(a)}
                      title="Preview asset"
                      aria-label={`Preview ${a.name}`}
                    >
                      <Eye />
                    </Button>
                    <Button variant="outline" size="xs" onClick={() => onAdd(a)}>
                      Add
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {filtered.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-8 text-center text-sm text-muted-foreground">
                  No assets found
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </ModalShell>

      {previewing && (
        <AssetPreviewModal
          assetId={previewing.id}
          assetName={previewing.name}
          contentType={previewing.content_type}
          sizeBytes={previewing.size_bytes}
          onClose={() => setPreviewing(null)}
        />
      )}
    </>
  );
}

function filterAssets(assets: Asset[], search: string): Asset[] {
  const q = search.toLowerCase();
  if (!q) return assets;
  return assets.filter(
    (a) =>
      (a.name ?? "").toLowerCase().includes(q) ||
      (a.description ?? "").toLowerCase().includes(q) ||
      (a.tags ?? []).some((t) => t.toLowerCase().includes(q)),
  );
}

function sortAssets(assets: Asset[], sortBy: SortKey, sortDir: "asc" | "desc"): Asset[] {
  return [...assets].sort((a, b) => {
    const cmp =
      sortBy === "name"
        ? a.name.localeCompare(b.name)
        : new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
    return sortDir === "asc" ? cmp : -cmp;
  });
}
