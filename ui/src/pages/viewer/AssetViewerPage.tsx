import { useState } from "react";
import { ArrowLeft, Share2, Pencil, Trash2, ChevronRight, ChevronLeft } from "lucide-react";
import { useAsset, useAssetContent, useUpdateAsset, useDeleteAsset } from "@/api/portal/hooks";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { ProvenancePanel } from "@/components/ProvenancePanel";
import { ShareDialog } from "@/components/ShareDialog";
import { formatBytes } from "@/lib/format";

interface Props {
  assetId: string;
  onNavigate: (path: string) => void;
}

export function AssetViewerPage({ assetId, onNavigate }: Props) {
  const { data: asset, isLoading } = useAsset(assetId);
  const { data: content } = useAssetContent(assetId);
  const updateAsset = useUpdateAsset();
  const deleteAsset = useDeleteAsset();
  const [shareOpen, setShareOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [editing, setEditing] = useState(false);
  const [editName, setEditName] = useState("");
  const [editDesc, setEditDesc] = useState("");
  const [editTags, setEditTags] = useState("");

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        Loading...
      </div>
    );
  }

  if (!asset) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
        <p>Asset not found</p>
        <button
          type="button"
          onClick={() => onNavigate("/")}
          className="mt-2 text-sm text-primary hover:underline"
        >
          Back to My Assets
        </button>
      </div>
    );
  }

  function startEdit() {
    if (!asset) return;
    setEditName(asset.name);
    setEditDesc(asset.description);
    setEditTags(asset.tags.join(", "));
    setEditing(true);
  }

  function saveEdit() {
    if (!asset) return;
    const tags = editTags
      .split(",")
      .map((t) => t.trim())
      .filter(Boolean);
    updateAsset.mutate(
      { id: asset.id, name: editName, description: editDesc, tags },
      { onSuccess: () => setEditing(false) },
    );
  }

  function handleDelete() {
    if (!asset || !confirm("Are you sure you want to delete this asset?")) return;
    deleteAsset.mutate(asset.id, { onSuccess: () => onNavigate("/") });
  }

  return (
    <div className="flex gap-4 h-full">
      {/* Content area */}
      <div className="flex-1 min-w-0 space-y-3">
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => onNavigate("/")}
            className="rounded-md p-1.5 hover:bg-accent"
          >
            <ArrowLeft className="h-4 w-4" />
          </button>
          <h2 className="text-lg font-semibold truncate flex-1">{asset.name}</h2>
          <button
            type="button"
            onClick={() => setShareOpen(true)}
            className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            <Share2 className="h-3.5 w-3.5" />
            Share
          </button>
          <button
            type="button"
            onClick={() => setSidebarOpen(!sidebarOpen)}
            className="rounded-md p-1.5 hover:bg-accent"
            title={sidebarOpen ? "Hide details" : "Show details"}
          >
            {sidebarOpen ? <ChevronRight className="h-4 w-4" /> : <ChevronLeft className="h-4 w-4" />}
          </button>
        </div>

        {content !== undefined ? (
          <ContentRenderer contentType={asset.content_type} content={content} />
        ) : (
          <div className="flex items-center justify-center py-12 text-muted-foreground">
            Loading content...
          </div>
        )}
      </div>

      {/* Metadata sidebar */}
      {sidebarOpen && (
        <div className="w-80 shrink-0 space-y-4 rounded-lg border bg-card p-4 overflow-auto">
          {editing ? (
            <div className="space-y-3">
              <div>
                <label className="text-xs font-medium text-muted-foreground">Name</label>
                <input
                  type="text"
                  value={editName}
                  onChange={(e) => setEditName(e.target.value)}
                  className="mt-1 w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
                />
              </div>
              <div>
                <label className="text-xs font-medium text-muted-foreground">Description</label>
                <textarea
                  value={editDesc}
                  onChange={(e) => setEditDesc(e.target.value)}
                  rows={3}
                  className="mt-1 w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2 resize-none"
                />
              </div>
              <div>
                <label className="text-xs font-medium text-muted-foreground">Tags (comma-separated)</label>
                <input
                  type="text"
                  value={editTags}
                  onChange={(e) => setEditTags(e.target.value)}
                  className="mt-1 w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
                />
              </div>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={saveEdit}
                  disabled={updateAsset.isPending}
                  className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                >
                  Save
                </button>
                <button
                  type="button"
                  onClick={() => setEditing(false)}
                  className="rounded-md bg-secondary px-3 py-1.5 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
                >
                  Cancel
                </button>
              </div>
            </div>
          ) : (
            <>
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <h3 className="text-sm font-medium">Details</h3>
                  <div className="flex gap-1">
                    <button
                      type="button"
                      onClick={startEdit}
                      className="rounded p-1 hover:bg-accent"
                      title="Edit"
                    >
                      <Pencil className="h-3.5 w-3.5" />
                    </button>
                    <button
                      type="button"
                      onClick={handleDelete}
                      className="rounded p-1 hover:bg-destructive/10 text-destructive"
                      title="Delete"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>
                </div>
                {asset.description && (
                  <p className="text-sm text-muted-foreground">{asset.description}</p>
                )}
                <dl className="text-sm space-y-1.5">
                  <div className="flex justify-between">
                    <dt className="text-muted-foreground">Type</dt>
                    <dd className="font-mono text-xs">{asset.content_type}</dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-muted-foreground">Size</dt>
                    <dd>{formatBytes(asset.size_bytes)}</dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-muted-foreground">Created</dt>
                    <dd>{new Date(asset.created_at).toLocaleString()}</dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-muted-foreground">Updated</dt>
                    <dd>{new Date(asset.updated_at).toLocaleString()}</dd>
                  </div>
                </dl>
              </div>

              {asset.tags.length > 0 && (
                <div className="space-y-2">
                  <h3 className="text-sm font-medium">Tags</h3>
                  <div className="flex flex-wrap gap-1.5">
                    {asset.tags.map((tag) => (
                      <span
                        key={tag}
                        className="text-xs px-2 py-0.5 rounded-full bg-muted text-muted-foreground"
                      >
                        {tag}
                      </span>
                    ))}
                  </div>
                </div>
              )}

              <div className="border-t pt-4">
                <ProvenancePanel provenance={asset.provenance} />
              </div>
            </>
          )}
        </div>
      )}

      <ShareDialog assetId={assetId} open={shareOpen} onOpenChange={setShareOpen} />
    </div>
  );
}

