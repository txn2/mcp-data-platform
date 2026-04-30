import { useState } from "react";
import { Search, FolderOpen, Plus, LayoutGrid, List, Users, Globe } from "lucide-react";
import { useCollections, useCreateCollection } from "@/api/portal/hooks";
import { AuthImg } from "@/components/AuthImg";
import { CollectionThumbnailQueue } from "@/components/CollectionThumbnailQueue";

const VIEW_STORAGE_KEY = "asset-view-mode";
type ViewMode = "grid" | "table";

function getStoredViewMode(): ViewMode {
  const stored = localStorage.getItem(VIEW_STORAGE_KEY);
  return stored === "table" ? "table" : "grid";
}

interface Props {
  onNavigate: (path: string) => void;
}

export function CollectionsPage({ onNavigate }: Props) {
  const [search, setSearch] = useState("");
  const [viewMode, setViewMode] = useState<ViewMode>(getStoredViewMode);

  function toggleViewMode(mode: ViewMode) {
    setViewMode(mode);
    localStorage.setItem(VIEW_STORAGE_KEY, mode);
  }

  const { data, isLoading } = useCollections({ search: search || undefined });
  const createMutation = useCreateCollection();

  const collections = data?.data ?? [];

  async function handleCreate() {
    if (createMutation.isPending) return;
    const result = await createMutation.mutateAsync({
      name: "Untitled Collection",
    });
    onNavigate(`/collections/${result.id}/edit`);
  }

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
            placeholder="Search collections..."
            className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>
        <button
          onClick={() => void handleCreate()}
          disabled={createMutation.isPending}
          className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          <Plus className="h-4 w-4" />
          {createMutation.isPending ? "Creating..." : "New Collection"}
        </button>
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
      ) : collections.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <FolderOpen className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm font-medium">No collections yet</p>
          <p className="text-xs mt-1">
            Create a collection to organize your assets into curated groups.
          </p>
        </div>
      ) : viewMode === "grid" ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {collections.map((coll) => {
            const summary = data?.share_summaries?.[coll.id];
            const tags = coll.asset_tags ?? [];
            return (
              <button
                key={coll.id}
                type="button"
                onClick={() => onNavigate(`/collections/${coll.id}`)}
                className="relative flex flex-col items-start rounded-lg border bg-card text-left transition-colors hover:bg-accent/50 hover:border-primary/30 overflow-hidden"
              >
                <div className="w-full aspect-[4/3] bg-muted">
                  {coll.thumbnail_s3_key ? (
                    <AuthImg
                      src={`/api/v1/portal/collections/${coll.id}/thumbnail`}
                      alt=""
                      className="w-full h-full object-cover object-top"
                    />
                  ) : (
                    <div className="w-full h-full flex items-center justify-center">
                      <FolderOpen className="h-8 w-8 text-muted-foreground/30" />
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
                    <FolderOpen className="h-5 w-5 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium truncate flex-1">
                      {coll.name}
                    </span>
                  </div>
                  {coll.description && (
                    <p className="text-xs text-muted-foreground mb-2 line-clamp-2">
                      {coll.description}
                    </p>
                  )}
                  {tags.length > 0 && (
                    <div className="flex flex-wrap gap-1 mb-2">
                      {tags.slice(0, 4).map((t) => (
                        <span key={t} className="text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground">
                          {t}
                        </span>
                      ))}
                      {tags.length > 4 && (
                        <span className="text-xs text-muted-foreground">+{tags.length - 4}</span>
                      )}
                    </div>
                  )}
                  <div className="flex items-center justify-between w-full text-xs text-muted-foreground">
                    <span>{new Date(coll.created_at).toLocaleDateString()}</span>
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
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[35%]">Name</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[30%]">Tags</th>
                <th className="px-4 py-2.5 text-center font-medium text-muted-foreground w-[8%]">Shared</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[12%]">Created</th>
              </tr>
            </thead>
            <tbody>
              {collections.map((coll) => {
                const summary = data?.share_summaries?.[coll.id];
                const tags = coll.asset_tags ?? [];
                return (
                  <tr
                    key={coll.id}
                    onClick={() => onNavigate(`/collections/${coll.id}`)}
                    className="border-b last:border-0 cursor-pointer transition-colors hover:bg-accent/50"
                  >
                    <td className="px-4 py-2.5 max-w-0">
                      <div className="flex items-center gap-2">
                        <FolderOpen className="h-4 w-4 text-muted-foreground shrink-0" />
                        <div className="min-w-0 flex-1">
                          <span className="font-medium truncate block">{coll.name}</span>
                          {coll.description && (
                            <span className="text-xs text-muted-foreground truncate block">{coll.description}</span>
                          )}
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-2.5 max-w-0">
                      <div className="flex flex-wrap gap-1">
                        {tags.slice(0, 4).map((t) => (
                          <span key={t} className="text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground truncate max-w-[100px]">
                            {t}
                          </span>
                        ))}
                        {tags.length > 4 && (
                          <span className="text-xs text-muted-foreground">+{tags.length - 4}</span>
                        )}
                      </div>
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
                      {new Date(coll.created_at).toLocaleDateString()}
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
          Showing {collections.length} of {data.total} collections
        </p>
      )}

      <CollectionThumbnailQueue collections={collections} />
    </div>
  );
}
