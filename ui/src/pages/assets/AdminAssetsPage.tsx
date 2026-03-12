import { useState, useEffect } from "react";
import { Search, FileText, Image, Code, File, Users, Globe, Table2 } from "lucide-react";
import { useAdminAssets } from "@/api/admin/hooks";
import { formatBytes, formatOwner } from "@/lib/format";

interface Props {
  onNavigate: (path: string) => void;
}

function contentTypeIcon(ct: string) {
  const lower = ct.toLowerCase();
  if (lower.includes("csv")) return Table2;
  if (lower.includes("html") || lower.includes("jsx")) return Code;
  if (lower.includes("svg") || lower.includes("image")) return Image;
  if (lower.includes("markdown") || lower.includes("text")) return FileText;
  return File;
}

export function AdminAssetsPage({ onNavigate }: Props) {
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(timer);
  }, [search]);

  const { data, isLoading } = useAdminAssets({
    search: debouncedSearch || undefined,
  });

  const assets = data?.data ?? [];

  return (
    <div className="space-y-4">
      {/* Search */}
      <div className="relative max-w-md">
        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search by name, description, owner, or tag..."
          className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        />
      </div>

      {/* Results table */}
      {isLoading ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          Loading...
        </div>
      ) : assets.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <File className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm font-medium">No assets found</p>
        </div>
      ) : (
        <div className="rounded-lg border bg-card overflow-hidden">
          <table className="w-full text-sm table-fixed">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[40%]">Name</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[20%]">Owner</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[12%]">Type</th>
                <th className="px-4 py-2.5 text-right font-medium text-muted-foreground w-[8%]">Size</th>
                <th className="px-4 py-2.5 text-center font-medium text-muted-foreground w-[8%]">Shared</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[12%]">Created</th>
              </tr>
            </thead>
            <tbody>
              {assets.map((asset) => {
                const Icon = contentTypeIcon(asset.content_type);
                const summary = data?.share_summaries?.[asset.id];
                return (
                  <tr
                    key={asset.id}
                    onClick={() => onNavigate(`/admin/assets/${asset.id}`)}
                    className="border-b last:border-0 cursor-pointer transition-colors hover:bg-accent/50"
                  >
                    <td className="px-4 py-2.5 max-w-0">
                      <div className="flex items-center gap-2">
                        <Icon className="h-4 w-4 text-muted-foreground shrink-0" />
                        <span className="font-medium truncate">{asset.name}</span>
                      </div>
                    </td>
                    <td className="px-4 py-2.5 max-w-0">
                      <span className="text-muted-foreground truncate block">{formatOwner(asset)}</span>
                    </td>
                    <td className="px-4 py-2.5">
                      <span className="font-mono text-xs text-muted-foreground">{asset.content_type}</span>
                    </td>
                    <td className="px-4 py-2.5 text-right text-muted-foreground">
                      {formatBytes(asset.size_bytes)}
                    </td>
                    <td className="px-4 py-2.5">
                      <div className="flex justify-center gap-1.5">
                        {summary?.has_user_share && (
                          <span title="Shared with users"><Users className="h-3.5 w-3.5 text-muted-foreground" /></span>
                        )}
                        {summary?.has_public_link && (
                          <span title="Has public link"><Globe className="h-3.5 w-3.5 text-muted-foreground" /></span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-2.5 text-muted-foreground">
                      {new Date(asset.created_at).toLocaleDateString()}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {data && data.total > data.limit && (
        <p className="text-sm text-muted-foreground text-center">
          Showing {assets.length} of {data.total} assets
        </p>
      )}
    </div>
  );
}
