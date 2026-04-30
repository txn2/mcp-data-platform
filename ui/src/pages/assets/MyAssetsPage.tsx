import { useState } from "react";
import { Search, FileText, Image, Code, File, Users, Globe, Table2, LayoutGrid, List, FolderOpen, Eye } from "lucide-react";
import { useAssets } from "@/api/portal/hooks";
import { formatBytes } from "@/lib/format";
import { ThumbnailQueue } from "@/components/ThumbnailQueue";
import { AuthImg } from "@/components/AuthImg";
import { AssetPreviewModal } from "@/components/AssetPreviewModal";

const VIEW_STORAGE_KEY = "asset-view-mode";
type ViewMode = "grid" | "table";

function getStoredViewMode(): ViewMode {
  const stored = localStorage.getItem(VIEW_STORAGE_KEY);
  return stored === "table" ? "table" : "grid";
}

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

function contentTypeBadgeColor(ct: string) {
  const lower = ct.toLowerCase();
  if (lower.includes("csv")) return "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
  if (lower.includes("jsx") || lower.includes("react")) return "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300";
  if (lower.includes("html")) return "bg-orange-100 text-orange-700 dark:bg-orange-950 dark:text-orange-300";
  if (lower.includes("svg")) return "bg-green-100 text-green-700 dark:bg-green-950 dark:text-green-300";
  if (lower.includes("markdown")) return "bg-purple-100 text-purple-700 dark:bg-purple-950 dark:text-purple-300";
  return "bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300";
}

export function MyAssetsPage({ onNavigate }: Props) {
  const [search, setSearch] = useState("");
  const [contentType, setContentType] = useState("");
  const [tag, setTag] = useState("");
  const [viewMode, setViewMode] = useState<ViewMode>(getStoredViewMode);
  const [previewing, setPreviewing] = useState<{ id: string; name: string; contentType: string; sizeBytes: number } | null>(null);

  function toggleViewMode(mode: ViewMode) {
    setViewMode(mode);
    localStorage.setItem(VIEW_STORAGE_KEY, mode);
  }

  const { data, isLoading } = useAssets({
    content_type: contentType || undefined,
    tag: tag || undefined,
  });

  const assets = (data?.data ?? []).filter(
    (a) =>
      !search ||
      a.name.toLowerCase().includes(search.toLowerCase()) ||
      a.description.toLowerCase().includes(search.toLowerCase()),
  );

  return (
    <div className="space-y-4">
      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search assets..."
            className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>
        <select
          value={contentType}
          onChange={(e) => setContentType(e.target.value)}
          className="rounded-md border bg-background px-3 py-2 text-sm"
        >
          <option value="">All types</option>
          <option value="text/html">HTML</option>
          <option value="text/jsx">JSX</option>
          <option value="image/svg+xml">SVG</option>
          <option value="text/markdown">Markdown</option>
          <option value="text/csv">CSV</option>
        </select>
        <input
          type="text"
          value={tag}
          onChange={(e) => setTag(e.target.value)}
          placeholder="Filter by tag..."
          className="rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        />
        <div className="flex gap-0.5 rounded-md border p-0.5">
          <button
            onClick={() => toggleViewMode("grid")}
            title="Grid view"
            className={`rounded-sm p-1.5 transition-colors ${viewMode === "grid" ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground"}`}
          >
            <LayoutGrid className="h-4 w-4" />
          </button>
          <button
            onClick={() => toggleViewMode("table")}
            title="Table view"
            className={`rounded-sm p-1.5 transition-colors ${viewMode === "table" ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground"}`}
          >
            <List className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* Results */}
      {isLoading ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          Loading...
        </div>
      ) : assets.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <File className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm font-medium">No assets yet</p>
          <div className="mt-3 max-w-md text-center space-y-2">
            <p className="text-xs">
              Assets are interactive dashboards, visualizations, and documents
              created during your conversations.
            </p>
            <p className="text-xs">
              Try asking your assistant to <em>"create an interactive dashboard"</em> or{" "}
              <em>"save this as an asset"</em> to get started.
            </p>
          </div>
        </div>
      ) : viewMode === "grid" ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {assets.map((asset) => {
            const Icon = contentTypeIcon(asset.content_type);
            const summary = data?.share_summaries?.[asset.id];
            return (
              <button
                key={asset.id}
                type="button"
                onClick={() => onNavigate(`/assets/${asset.id}`)}
                className="relative flex flex-col items-start rounded-lg border bg-card text-left transition-colors hover:bg-accent/50 hover:border-primary/30 overflow-hidden"
              >
                <div className="w-full aspect-[4/3] bg-muted">
                  {asset.thumbnail_s3_key ? (
                    <AuthImg
                      src={`/api/v1/portal/assets/${asset.id}/thumbnail`}
                      alt=""
                      className="w-full h-full object-cover object-top"
                    />
                  ) : (
                    <div className="w-full h-full flex items-center justify-center">
                      <Icon className="h-8 w-8 text-muted-foreground/30" />
                    </div>
                  )}
                </div>
                <div className="p-4 w-full">
                  {summary && (summary.has_user_share || summary.has_public_link) && (
                    <div className="absolute top-2 right-2 flex gap-1 bg-background/80 rounded-full px-1.5 py-0.5">
                      {summary.has_user_share && (
                        <span title="Shared with users"><Users className="h-3.5 w-3.5 text-muted-foreground" /></span>
                      )}
                      {summary.has_public_link && (
                        <span title="Has public link"><Globe className="h-3.5 w-3.5 text-muted-foreground" /></span>
                      )}
                    </div>
                  )}
                  <div className="flex items-center gap-2 mb-2 w-full">
                    <Icon className="h-5 w-5 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium truncate flex-1">
                      {asset.name}
                    </span>
                  </div>
                  {asset.description && (
                    <p className="text-xs text-muted-foreground mb-2 line-clamp-2">
                      {asset.description}
                    </p>
                  )}
                  <div className="flex flex-wrap gap-1.5 mb-2">
                    <span
                      className={`text-xs px-1.5 py-0.5 rounded-full font-medium ${contentTypeBadgeColor(asset.content_type)}`}
                    >
                      {asset.content_type}
                    </span>
                    {asset.tags.slice(0, 3).map((t) => (
                      <span
                        key={t}
                        className="text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground"
                      >
                        {t}
                      </span>
                    ))}
                  </div>
                  {(asset.collections ?? []).length > 0 && (
                    <div className="flex flex-wrap gap-1 mb-2">
                      {(asset.collections ?? []).slice(0, 2).map((c) => (
                        <span key={c.id} className="text-xs px-1.5 py-0.5 rounded-full bg-primary/10 text-primary inline-flex items-center gap-0.5">
                          <FolderOpen className="h-2.5 w-2.5 shrink-0" />
                          {c.name}
                        </span>
                      ))}
                    </div>
                  )}
                  <div className="flex items-center justify-between w-full text-xs text-muted-foreground">
                    <span>{formatBytes(asset.size_bytes)}</span>
                    <span>{new Date(asset.created_at).toLocaleDateString()}</span>
                  </div>
                </div>
              </button>
            );
          })}
        </div>
        ) : (
        <div className="rounded-lg border bg-card overflow-hidden">
          <table className="w-full text-sm table-fixed">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[28%]">Name</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[10%]">Type</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[15%]">Tags</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[15%]">Collections</th>
                <th className="px-4 py-2.5 text-right font-medium text-muted-foreground w-[8%]">Size</th>
                <th className="px-4 py-2.5 text-center font-medium text-muted-foreground w-[8%]">Shared</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[10%]">Created</th>
                <th className="px-4 py-2.5 w-[4%]" />
              </tr>
            </thead>
            <tbody>
              {assets.map((asset) => {
                const Icon = contentTypeIcon(asset.content_type);
                const summary = data?.share_summaries?.[asset.id];
                return (
                  <tr
                    key={asset.id}
                    onClick={() => onNavigate(`/assets/${asset.id}`)}
                    className="border-b last:border-0 cursor-pointer transition-colors hover:bg-accent/50"
                  >
                    <td className="px-4 py-2.5 max-w-0">
                      <div className="flex items-center gap-2">
                        <Icon className="h-4 w-4 text-muted-foreground shrink-0" />
                        <div className="min-w-0 flex-1">
                          <span className="font-medium truncate block">{asset.name}</span>
                          {asset.description && (
                            <span className="text-xs text-muted-foreground truncate block">{asset.description}</span>
                          )}
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-2.5">
                      <span className={`text-xs px-1.5 py-0.5 rounded-full font-medium whitespace-nowrap ${contentTypeBadgeColor(asset.content_type)}`}>
                        {asset.content_type}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 max-w-0">
                      <div className="flex flex-wrap gap-1">
                        {asset.tags.slice(0, 3).map((t) => (
                          <span
                            key={t}
                            className="text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground truncate max-w-[100px]"
                          >
                            {t}
                          </span>
                        ))}
                        {asset.tags.length > 3 && (
                          <span className="text-xs text-muted-foreground">+{asset.tags.length - 3}</span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-2.5 max-w-0">
                      <div className="flex flex-wrap gap-1">
                        {(asset.collections ?? []).slice(0, 2).map((c) => (
                          <span
                            key={c.id}
                            className="text-xs px-1.5 py-0.5 rounded-full bg-primary/10 text-primary truncate max-w-[100px] inline-flex items-center gap-0.5"
                            onClick={(e) => { e.stopPropagation(); onNavigate(`/collections/${c.id}`); }}
                            role="button"
                            tabIndex={0}
                            onKeyDown={(e) => { if (e.key === "Enter") { e.stopPropagation(); onNavigate(`/collections/${c.id}`); } }}
                          >
                            <FolderOpen className="h-2.5 w-2.5 shrink-0" />
                            {c.name}
                          </span>
                        ))}
                        {(asset.collections ?? []).length > 2 && (
                          <span className="text-xs text-muted-foreground">+{(asset.collections ?? []).length - 2}</span>
                        )}
                      </div>
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
                    <td className="px-2 py-2.5">
                      <button
                        onClick={(e) => { e.stopPropagation(); setPreviewing({ id: asset.id, name: asset.name, contentType: asset.content_type, sizeBytes: asset.size_bytes }); }}
                        className="rounded p-1 text-muted-foreground hover:text-foreground hover:bg-accent"
                        title="Quick preview"
                      >
                        <Eye className="h-3.5 w-3.5" />
                      </button>
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

      <ThumbnailQueue assets={assets} />

      {previewing && (
        <AssetPreviewModal
          assetId={previewing.id}
          assetName={previewing.name}
          contentType={previewing.contentType}
          sizeBytes={previewing.sizeBytes}
          onClose={() => setPreviewing(null)}
        />
      )}
    </div>
  );
}
