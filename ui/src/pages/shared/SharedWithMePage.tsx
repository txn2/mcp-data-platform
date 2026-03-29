import { useState } from "react";
import { Search, FileText, Image, Code, File, Table2 } from "lucide-react";
import { useSharedWithMe } from "@/api/portal/hooks";
import { formatBytes } from "@/lib/format";
import { ThumbnailQueue } from "@/components/ThumbnailQueue";
import { AuthImg } from "@/components/AuthImg";

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

export function SharedWithMePage({ onNavigate }: Props) {
  const [search, setSearch] = useState("");
  const [contentType, setContentType] = useState("");
  const [tag, setTag] = useState("");

  const { data, isLoading } = useSharedWithMe();

  const items = (data?.data ?? []).filter(
    (item) =>
      !search ||
      item.asset.name.toLowerCase().includes(search.toLowerCase()) ||
      item.asset.description.toLowerCase().includes(search.toLowerCase()),
  );

  const filteredItems = items.filter(
    (item) =>
      (!contentType || item.asset.content_type === contentType) &&
      (!tag || item.asset.tags.some((t) => t.toLowerCase().includes(tag.toLowerCase()))),
  );

  const assets = filteredItems.map((item) => item.asset);

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
            placeholder="Search shared assets..."
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
      </div>

      {/* Results */}
      {isLoading ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          Loading...
        </div>
      ) : filteredItems.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <File className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm">No shared assets</p>
          <p className="text-xs mt-1">
            Assets shared with you will appear here.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {filteredItems.map((item) => {
            const Icon = contentTypeIcon(item.asset.content_type);
            return (
              <button
                key={item.share_id}
                type="button"
                onClick={() => onNavigate(`/assets/${item.asset.id}`)}
                className="relative flex flex-col items-start rounded-lg border bg-card text-left transition-colors hover:bg-accent/50 hover:border-primary/30 overflow-hidden"
              >
                <div className="w-full aspect-[4/3] bg-muted">
                  {item.asset.thumbnail_s3_key ? (
                    <AuthImg
                      src={`/api/v1/portal/assets/${item.asset.id}/thumbnail`}
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
                  <div className="flex items-center gap-2 mb-2 w-full">
                    <Icon className="h-5 w-5 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium truncate flex-1">
                      {item.asset.name}
                    </span>
                  </div>
                  {item.asset.description && (
                    <p className="text-xs text-muted-foreground mb-2 line-clamp-2">
                      {item.asset.description}
                    </p>
                  )}
                  <div className="flex flex-wrap gap-1.5 mb-2">
                    <span
                      className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium ${contentTypeBadgeColor(item.asset.content_type)}`}
                    >
                      {item.asset.content_type}
                    </span>
                    {item.asset.tags.slice(0, 3).map((t) => (
                      <span
                        key={t}
                        className="text-[10px] px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground"
                      >
                        {t}
                      </span>
                    ))}
                  </div>
                  <div className="flex items-center justify-between w-full text-xs text-muted-foreground">
                    <span className="flex items-center gap-1.5">
                      Shared by {item.shared_by}
                      <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium ${item.permission === "editor" ? "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300" : "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400"}`}>
                        {item.permission === "editor" ? "Editor" : "Viewer"}
                      </span>
                    </span>
                    <span>{formatBytes(item.asset.size_bytes)}</span>
                  </div>
                  <div className="flex items-center justify-between w-full text-xs text-muted-foreground mt-1">
                    <span>{new Date(item.shared_at).toLocaleDateString()}</span>
                  </div>
                </div>
              </button>
            );
          })}
        </div>
      )}

      {data && data.total > data.limit && (
        <p className="text-sm text-muted-foreground text-center">
          Showing {filteredItems.length} of {data.total} shared assets
        </p>
      )}

      <ThumbnailQueue assets={assets} />
    </div>
  );
}
