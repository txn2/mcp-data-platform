import { useState } from "react";
import { Search, FileText, Image, Code, File, Users, Globe } from "lucide-react";
import { useAssets } from "@/api/portal/hooks";
import { formatBytes } from "@/lib/format";

interface Props {
  onNavigate: (path: string) => void;
}

function contentTypeIcon(ct: string) {
  const lower = ct.toLowerCase();
  if (lower.includes("html") || lower.includes("jsx")) return Code;
  if (lower.includes("svg") || lower.includes("image")) return Image;
  if (lower.includes("markdown") || lower.includes("text")) return FileText;
  return File;
}

function contentTypeBadgeColor(ct: string) {
  const lower = ct.toLowerCase();
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
        </select>
        <input
          type="text"
          value={tag}
          onChange={(e) => setTag(e.target.value)}
          placeholder="Filter by tag..."
          className="rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        />
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
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {assets.map((asset) => {
            const Icon = contentTypeIcon(asset.content_type);
            const summary = data?.share_summaries?.[asset.id];
            return (
              <button
                key={asset.id}
                type="button"
                onClick={() => onNavigate(`/assets/${asset.id}`)}
                className="relative flex flex-col items-start rounded-lg border bg-card p-4 text-left transition-colors hover:bg-accent/50 hover:border-primary/30"
              >
                {summary && (summary.has_user_share || summary.has_public_link) && (
                  <div className="absolute top-2 right-2 flex gap-1">
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
                    className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium ${contentTypeBadgeColor(asset.content_type)}`}
                  >
                    {asset.content_type}
                  </span>
                  {asset.tags.slice(0, 3).map((t) => (
                    <span
                      key={t}
                      className="text-[10px] px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground"
                    >
                      {t}
                    </span>
                  ))}
                </div>
                <div className="flex items-center justify-between w-full text-xs text-muted-foreground">
                  <span>{formatBytes(asset.size_bytes)}</span>
                  <span>{new Date(asset.created_at).toLocaleDateString()}</span>
                </div>
              </button>
            );
          })}
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
