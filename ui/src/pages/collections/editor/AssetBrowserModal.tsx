import { useState, useMemo } from "react";
import { FileText, Eye, Search, X, ArrowUpDown } from "lucide-react";
import type { Asset } from "@/api/portal/types";
import { AuthImg } from "@/components/AuthImg";
import { AssetPreviewModal } from "@/components/AssetPreviewModal";

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
  const [sortBy, setSortBy] = useState<"name" | "created_at">("name");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");
  const [previewing, setPreviewing] = useState<Asset | null>(null);

  function toggleSort(col: "name" | "created_at") {
    if (sortBy === col) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(col);
      setSortDir(col === "name" ? "asc" : "desc");
    }
  }

  const filtered = useMemo(() => {
    let list = assets;
    if (search) {
      const q = search.toLowerCase();
      list = list.filter(
        (a) =>
          (a.name ?? "").toLowerCase().includes(q) ||
          (a.description ?? "").toLowerCase().includes(q) ||
          (a.tags ?? []).some((t) => t.toLowerCase().includes(q)),
      );
    }
    list = [...list].sort((a, b) => {
      const cmp = sortBy === "name"
        ? a.name.localeCompare(b.name)
        : new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
      return sortDir === "asc" ? cmp : -cmp;
    });
    return list;
  }, [assets, search, sortBy, sortDir]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} role="button" tabIndex={-1} aria-label="Close" onKeyDown={(e) => { if (e.key === "Escape") onClose(); }} />
      <div className="relative rounded-lg border bg-card shadow-lg w-full max-w-2xl mx-4 max-h-[80vh] flex flex-col">
        <div className="flex items-center gap-3 p-4 border-b">
          <h3 className="text-sm font-semibold flex-1">Add Assets</h3>
          <button onClick={onClose} className="rounded-md p-1 hover:bg-accent text-muted-foreground hover:text-foreground">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="p-4 pb-2">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search by name, description, or tag..."
              autoFocus
              className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
            />
          </div>
        </div>
        <div className="flex-1 overflow-auto px-4 pb-4">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-card">
              <tr className="border-b">
                <th className="w-10" />
                <th className="py-2 text-left font-medium text-muted-foreground">
                  <button onClick={() => toggleSort("name")} className="flex items-center gap-1 hover:text-foreground">
                    Name <ArrowUpDown className="h-3 w-3" />
                  </button>
                </th>
                <th className="py-2 text-left font-medium text-muted-foreground w-[20%]">Type</th>
                <th className="py-2 text-left font-medium text-muted-foreground w-[25%]">
                  <button onClick={() => toggleSort("created_at")} className="flex items-center gap-1 hover:text-foreground">
                    Created <ArrowUpDown className="h-3 w-3" />
                  </button>
                </th>
                <th className="w-20" />
              </tr>
            </thead>
            <tbody>
              {filtered.map((a) => (
                <tr key={a.id} className="border-b last:border-0 hover:bg-accent/50">
                  <td className="py-2 pr-2">
                    {a.thumbnail_s3_key ? (
                      <AuthImg src={`/api/v1/portal/assets/${a.id}/thumbnail`} alt="" className="w-8 h-6 rounded object-cover" />
                    ) : (
                      <div className="w-8 h-6 rounded bg-muted flex items-center justify-center">
                        <FileText className="h-3 w-3 text-muted-foreground/50" />
                      </div>
                    )}
                  </td>
                  <td className="py-2 max-w-0">
                    <span className="font-medium truncate block">{a.name}</span>
                    {(a.tags ?? []).length > 0 && (
                      <div className="flex gap-1 mt-0.5">
                        {(a.tags ?? []).slice(0, 3).map((t) => (
                          <span key={t} className="text-xs px-1 py-0.5 rounded bg-muted text-muted-foreground">{t}</span>
                        ))}
                      </div>
                    )}
                  </td>
                  <td className="py-2 text-muted-foreground text-xs">{a.content_type}</td>
                  <td className="py-2 text-muted-foreground text-xs">{new Date(a.created_at).toLocaleDateString()}</td>
                  <td className="py-2">
                    <div className="flex items-center gap-1.5">
                      <button
                        onClick={() => setPreviewing(a)}
                        className="rounded bg-muted text-muted-foreground px-2 py-0.5 text-xs font-medium hover:bg-muted/80 hover:text-foreground"
                        title="Preview asset"
                      >
                        <Eye className="h-3 w-3" />
                      </button>
                      <button
                        onClick={() => onAdd(a)}
                        className="rounded bg-primary/10 text-primary px-2 py-0.5 text-xs font-medium hover:bg-primary/20"
                      >
                        Add
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {filtered.length === 0 && (
                <tr>
                  <td colSpan={5} className="py-8 text-center text-muted-foreground text-sm">
                    No assets found
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      {previewing && (
        <AssetPreviewModal
          assetId={previewing.id}
          assetName={previewing.name}
          contentType={previewing.content_type}
          sizeBytes={previewing.size_bytes}
          onClose={() => setPreviewing(null)}
        />
      )}
    </div>
  );
}
