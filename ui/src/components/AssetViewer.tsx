import { useState, useCallback, useEffect, lazy, Suspense, type ReactNode } from "react";
import { ArrowLeft, Share2, Pencil, Trash2, Download, ChevronRight, ChevronLeft, AlertTriangle, Save, Eye, Code, Copy } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import type { Asset, SharePermission } from "@/api/portal/types";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { ProvenancePanel } from "@/components/ProvenancePanel";
import { ShareDialog } from "@/components/ShareDialog";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { ThumbnailGenerator } from "@/components/ThumbnailGenerator";
import { isThumbnailSupported } from "@/lib/thumbnail";
import { formatBytes } from "@/lib/format";

const SourceEditor = lazy(() =>
  import("@/components/SourceEditor").then((m) => ({ default: m.SourceEditor })),
);

interface MutationLike<TVariables> {
  mutate: (vars: TVariables, options?: { onSuccess?: () => void; onError?: () => void }) => void;
  isPending: boolean;
}

type ViewMode = "preview" | "source";

interface AssetViewerProps {
  asset: Asset | undefined;
  content: string | ArrayBuffer | undefined;
  isLoading: boolean;
  contentUrl: string;
  backPath: string;
  backLabel: string;
  onNavigate: (path: string) => void;
  updateMutation: MutationLike<{ id: string; name: string; description: string; tags: string[] }>;
  deleteMutation: MutationLike<string>;
  contentUpdateMutation?: MutationLike<{ id: string; content: string }>;
  copyMutation?: MutationLike<string>;
  isOwner?: boolean;
  sharePermission?: SharePermission;
  toolbarExtra?: ReactNode;
  detailRows?: { label: string; value: ReactNode }[];
}

function isTextContent(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return ct.includes("text/") || ct.includes("html") || ct.includes("svg") ||
    ct.includes("xml") || ct.includes("json") || ct.includes("javascript") ||
    ct.includes("jsx") || ct.includes("markdown");
}

export function AssetViewer({
  asset,
  content,
  isLoading,
  contentUrl,
  backPath,
  backLabel,
  onNavigate,
  updateMutation,
  deleteMutation,
  contentUpdateMutation,
  copyMutation,
  isOwner = true,
  sharePermission,
  toolbarExtra,
  detailRows,
}: AssetViewerProps) {
  const [shareOpen, setShareOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editName, setEditName] = useState("");
  const [editDesc, setEditDesc] = useState("");
  const [editTags, setEditTags] = useState("");
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [sharedSaveWarningOpen, setSharedSaveWarningOpen] = useState(false);

  const [viewMode, setViewMode] = useState<ViewMode>("preview");
  const [editedContent, setEditedContent] = useState<string>("");
  const [dirty, setDirty] = useState(false);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">("idle");
  const [thumbnailStale, setThumbnailStale] = useState(false);
  const isSharedEditor = !isOwner && sharePermission === "editor";

  const canEditSource = !!contentUpdateMutation && !!asset && isTextContent(asset.content_type);
  const contentStr = typeof content === "string" ? content : "";
  const hasChanges = dirty && editedContent !== contentStr;

  // Only sync editedContent when the server content changes (initial load or post-save refetch),
  // NOT on tab switches — so unsaved edits survive Preview/Source toggling.
  useEffect(() => {
    if (content !== undefined) {
      setEditedContent(contentStr);
      setDirty(false);
      setSaveStatus("idle");
    }
  }, [contentStr]); // eslint-disable-line react-hooks/exhaustive-deps

  const doSaveContent = useCallback(() => {
    if (!asset || !contentUpdateMutation) return;
    setSaveStatus("idle");
    contentUpdateMutation.mutate(
      { id: asset.id, content: editedContent },
      {
        onSuccess: () => {
          setSaveStatus("saved");
          setSharedSaveWarningOpen(false);
          if (isThumbnailSupported(asset.content_type)) {
            setThumbnailStale(true);
          }
        },
        onError: () => setSaveStatus("error"),
      },
    );
  }, [asset, contentUpdateMutation, editedContent]);

  const handleSaveContent = useCallback(() => {
    if (isSharedEditor) {
      setSharedSaveWarningOpen(true);
      return;
    }
    doSaveContent();
  }, [isSharedEditor, doSaveContent]);

  const handleCopyToMyAssets = useCallback(() => {
    if (!asset || !copyMutation) return;
    copyMutation.mutate(asset.id, {
      onSuccess: () => {
        onNavigate("/");
      },
    });
  }, [asset, copyMutation, onNavigate]);

  const handleDownload = useCallback(async () => {
    if (!asset) return;
    try {
      const res = await fetch(contentUrl, { credentials: "include" });
      if (!res.ok) return;
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = asset.name;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch {
      // best-effort download
    }
  }, [asset, contentUrl]);

  if (isLoading) {
    return <LoadingIndicator />;
  }

  if (!asset) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
        <p>Asset not found</p>
        <button
          type="button"
          onClick={() => onNavigate(backPath)}
          className="mt-2 text-sm text-primary hover:underline"
        >
          {backLabel}
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
    updateMutation.mutate(
      { id: asset.id, name: editName, description: editDesc, tags },
      { onSuccess: () => setEditing(false) },
    );
  }

  function confirmDelete() {
    if (!asset) return;
    deleteMutation.mutate(asset.id, {
      onSuccess: () => {
        setDeleteModalOpen(false);
        onNavigate(backPath);
      },
    });
  }

  return (
    <div className="flex gap-4 h-full">
      {/* Content area */}
      <div className="flex-1 min-w-0 space-y-3">
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => onNavigate(backPath)}
            className="rounded-md p-1.5 hover:bg-accent"
          >
            <ArrowLeft className="h-4 w-4" />
          </button>
          <h2 className="text-lg font-semibold truncate flex-1 min-w-0">{asset.name}</h2>
          {toolbarExtra}
          {!isOwner && (
            <span className="text-xs px-2 py-1 rounded-full bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300">
              Shared{sharePermission === "editor" ? " (Editor)" : " (Viewer)"}
            </span>
          )}
          {isOwner && (
            <button
              type="button"
              onClick={() => setDeleteModalOpen(true)}
              className="flex items-center gap-1.5 rounded-md border border-destructive/30 px-3 py-1.5 text-sm font-medium text-destructive hover:bg-destructive/10"
            >
              <Trash2 className="h-3.5 w-3.5" />
              Delete
            </button>
          )}
          <button
            type="button"
            onClick={handleDownload}
            className="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent"
            title="Download"
          >
            <Download className="h-3.5 w-3.5" />
            Download
          </button>
          {copyMutation && (
            <button
              type="button"
              onClick={handleCopyToMyAssets}
              disabled={copyMutation.isPending}
              className="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent disabled:opacity-50"
              title="Save an independent copy to My Assets"
            >
              <Copy className="h-3.5 w-3.5" />
              {copyMutation.isPending ? "Copying..." : "Save to My Assets"}
            </button>
          )}
          {isOwner && (
            <button
              type="button"
              onClick={() => setShareOpen(true)}
              className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
            >
              <Share2 className="h-3.5 w-3.5" />
              Share
            </button>
          )}
          <button
            type="button"
            onClick={() => setSidebarOpen(!sidebarOpen)}
            className="rounded-md p-1.5 hover:bg-accent"
            title={sidebarOpen ? "Hide details" : "Show details"}
          >
            {sidebarOpen ? <ChevronRight className="h-4 w-4" /> : <ChevronLeft className="h-4 w-4" />}
          </button>
        </div>

        {/* View mode toggle + save button */}
        {canEditSource && (
          <div className="flex items-center gap-2">
            <div className="inline-flex rounded-md border text-sm">
              <button
                type="button"
                onClick={() => setViewMode("preview")}
                className={`flex items-center gap-1.5 px-3 py-1.5 rounded-l-md transition-colors ${viewMode === "preview" ? "bg-accent font-medium" : "hover:bg-accent/50"}`}
              >
                <Eye className="h-3.5 w-3.5" />
                Preview
              </button>
              <button
                type="button"
                onClick={() => setViewMode("source")}
                className={`flex items-center gap-1.5 px-3 py-1.5 rounded-r-md border-l transition-colors ${viewMode === "source" ? "bg-accent font-medium" : "hover:bg-accent/50"}`}
              >
                <Code className="h-3.5 w-3.5" />
                Source
              </button>
            </div>
            {viewMode === "source" && (
              <>
                <button
                  type="button"
                  onClick={handleSaveContent}
                  disabled={!hasChanges || contentUpdateMutation?.isPending}
                  className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                >
                  <Save className="h-3.5 w-3.5" />
                  {contentUpdateMutation?.isPending ? "Saving..." : "Save"}
                </button>
                {saveStatus === "saved" && (
                  <span className="text-xs text-green-600 dark:text-green-400">Saved</span>
                )}
                {saveStatus === "error" && (
                  <span className="text-xs text-destructive">Save failed</span>
                )}
              </>
            )}
          </div>
        )}

        {content !== undefined ? (
          <>
            {canEditSource && (
              <div style={{ display: viewMode === "source" ? undefined : "none" }}>
                <Suspense fallback={<LoadingIndicator />}>
                  <SourceEditor
                    content={editedContent}
                    contentType={asset.content_type}
                    onChange={(v) => { setEditedContent(v); setDirty(true); }}
                  />
                </Suspense>
              </div>
            )}
            {(viewMode !== "source" || !canEditSource) && (
              <ContentRenderer contentType={asset.content_type} content={hasChanges ? editedContent : (content as string)} fileName={asset.name} />
            )}
          </>
        ) : (
          <LoadingIndicator />
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
                  disabled={updateMutation.isPending}
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
                  {isOwner && (
                    <button
                      type="button"
                      onClick={startEdit}
                      className="rounded p-1 hover:bg-accent"
                      title="Edit"
                    >
                      <Pencil className="h-3.5 w-3.5" />
                    </button>
                  )}
                </div>
                {asset.description && (
                  <p className="text-sm text-muted-foreground">{asset.description}</p>
                )}
                <dl className="text-sm space-y-1.5">
                  {detailRows?.map((row) => (
                    <div key={row.label} className="flex justify-between">
                      <dt className="text-muted-foreground">{row.label}</dt>
                      <dd className="text-xs truncate max-w-[160px]">{row.value}</dd>
                    </div>
                  ))}
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

      {content && typeof content === "string" && isThumbnailSupported(asset.content_type) && (!asset.thumbnail_s3_key || thumbnailStale) && (
        <ThumbnailGeneratorWithInvalidation
          key={thumbnailStale ? "regen" : "initial"}
          assetId={asset.id}
          content={thumbnailStale ? editedContent : content}
          contentType={asset.content_type}
          onDone={() => setThumbnailStale(false)}
        />
      )}

      <ShareDialog assetId={asset.id} open={shareOpen} onOpenChange={setShareOpen} />

      {/* Delete confirmation modal */}
      {deleteModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={() => setDeleteModalOpen(false)}
            onKeyDown={(e) => { if (e.key === "Escape") setDeleteModalOpen(false); }}
            role="button"
            tabIndex={-1}
            aria-label="Close"
          />
          <div className="relative rounded-lg border bg-card p-6 shadow-lg max-w-sm w-full mx-4 space-y-4">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-destructive/10">
                <AlertTriangle className="h-5 w-5 text-destructive" />
              </div>
              <div>
                <h3 className="text-sm font-semibold">Delete asset</h3>
                <p className="text-sm text-muted-foreground">This action cannot be undone.</p>
              </div>
            </div>
            <p className="text-sm">
              Are you sure you want to delete <span className="font-medium">{asset.name}</span>?
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setDeleteModalOpen(false)}
                className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={confirmDelete}
                disabled={deleteMutation.isPending}
                className="rounded-md bg-destructive px-4 py-2 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
              >
                {deleteMutation.isPending ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Shared asset save warning modal */}
      {sharedSaveWarningOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={() => setSharedSaveWarningOpen(false)}
            onKeyDown={(e) => { if (e.key === "Escape") setSharedSaveWarningOpen(false); }}
            role="button"
            tabIndex={-1}
            aria-label="Close"
          />
          <div className="relative rounded-lg border bg-card p-6 shadow-lg max-w-sm w-full mx-4 space-y-4">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-amber-100 dark:bg-amber-950">
                <AlertTriangle className="h-5 w-5 text-amber-600 dark:text-amber-400" />
              </div>
              <div>
                <h3 className="text-sm font-semibold">Editing a shared asset</h3>
                <p className="text-sm text-muted-foreground">
                  You are editing a shared asset owned by {asset.owner_email || "another user"}.
                  Changes will be visible to the owner and all other recipients.
                </p>
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setSharedSaveWarningOpen(false)}
                className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={doSaveContent}
                disabled={contentUpdateMutation?.isPending}
                className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
              >
                {contentUpdateMutation?.isPending ? "Saving..." : "Save Changes"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function ThumbnailGeneratorWithInvalidation({
  assetId,
  content,
  contentType,
  onDone,
}: {
  assetId: string;
  content: string;
  contentType: string;
  onDone?: () => void;
}) {
  const qc = useQueryClient();
  const handleCaptured = useCallback(() => {
    void qc.invalidateQueries({ queryKey: ["asset", assetId] });
    void qc.invalidateQueries({ queryKey: ["assets"] });
    onDone?.();
  }, [qc, assetId, onDone]);

  const handleFailed = useCallback(() => {
    onDone?.();
  }, [onDone]);

  return (
    <ThumbnailGenerator
      assetId={assetId}
      content={content}
      contentType={contentType}
      onCaptured={handleCaptured}
      onFailed={handleFailed}
    />
  );
}
