import { useState, useCallback, useEffect } from "react";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { ShareDialog } from "@/components/ShareDialog";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { isThumbnailSupported } from "@/lib/thumbnail";
import { isTextContent, type AssetViewerProps, type ViewMode } from "./assetviewer/types";
import { ThumbnailGeneratorWithInvalidation } from "./assetviewer/ThumbnailGeneratorWithInvalidation";
import { AssetViewerToolbar } from "./assetviewer/AssetViewerToolbar";
import { AssetContentView } from "./assetviewer/AssetContentView";
import { AssetMetadataSidebar } from "./assetviewer/AssetMetadataSidebar";
import { AssetViewerModals } from "./assetviewer/AssetViewerModals";

export type { AssetViewerProps } from "./assetviewer/types";

export function AssetViewer({
  asset,
  content,
  isLoading,
  contentUrl,
  onBack,
  onNavigate,
  updateMutation,
  deleteMutation,
  contentUpdateMutation,
  copyMutation,
  isOwner = true,
  sharePermission,
  toolbarExtra,
  detailRows,
  versions,
  versionsLoading,
  revertMutation,
  selectedVersion,
  onSelectVersion,
  versionContent,
  versionContentLoading,
}: AssetViewerProps) {
  const [shareOpen, setShareOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editName, setEditName] = useState("");
  const [editDesc, setEditDesc] = useState("");
  const [editTags, setEditTags] = useState("");
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [sharedSaveWarningOpen, setSharedSaveWarningOpen] = useState(false);
  const [changeSummaryOpen, setChangeSummaryOpen] = useState(false);
  const [changeSummary, setChangeSummary] = useState("");
  const [revertModalOpen, setRevertModalOpen] = useState(false);

  const [viewMode, setViewMode] = useState<ViewMode>("preview");
  const [editedContent, setEditedContent] = useState<string>("");
  const [dirty, setDirty] = useState(false);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">("idle");
  const [thumbnailStale, setThumbnailStale] = useState(false);
  const isSharedEditor = !isOwner && sharePermission === "editor";

  const canEditSource = !!contentUpdateMutation && !!asset && isTextContent(asset.content_type);
  const contentStr = typeof content === "string" ? content : "";
  const hasChanges = dirty && editedContent !== contentStr;

  const viewingOldVersion = selectedVersion != null && asset != null && selectedVersion !== asset.current_version;

  // Only sync editedContent when the server content changes (initial load or post-save refetch),
  // NOT on tab switches — so unsaved edits survive Preview/Source toggling.
  useEffect(() => {
    if (content !== undefined) {
      setEditedContent(contentStr);
      setDirty(false);
      setSaveStatus("idle");
    }
  }, [contentStr]); // eslint-disable-line react-hooks/exhaustive-deps

  const doSaveContent = useCallback((summary?: string) => {
    if (!asset || !contentUpdateMutation) return;
    setSaveStatus("idle");
    contentUpdateMutation.mutate(
      { id: asset.id, content: editedContent, changeSummary: summary || undefined },
      {
        onSuccess: () => {
          setSaveStatus("saved");
          setSharedSaveWarningOpen(false);
          setChangeSummaryOpen(false);
          setChangeSummary("");
          setViewMode("preview");
          onSelectVersion?.(null);
          if (isThumbnailSupported(asset.content_type)) {
            setThumbnailStale(true);
          }
        },
        onError: () => setSaveStatus("error"),
      },
    );
  }, [asset, contentUpdateMutation, editedContent, onSelectVersion]);

  const handleSaveContent = useCallback(() => {
    if (isSharedEditor) {
      setSharedSaveWarningOpen(true);
      return;
    }
    setChangeSummaryOpen(true);
  }, [isSharedEditor]);

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
          onClick={onBack}
          className="mt-2 text-sm text-primary hover:underline"
        >
          Back
        </button>
      </div>
    );
  }

  function startEdit() {
    if (!asset) return;
    setEditName(asset.name);
    setEditDesc(asset.description ?? "");
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
        onBack();
      },
    });
  }

  function handleConfirmRevert() {
    if (revertMutation && asset && selectedVersion != null) {
      revertMutation.mutate({ assetId: asset.id, version: selectedVersion }, {
        onSuccess: () => {
          setRevertModalOpen(false);
          onSelectVersion?.(null);
        },
      });
    }
  }

  return (
    <div className="flex gap-4 h-full">
      {/* Content area */}
      <div className="flex-1 min-w-0 space-y-3">
        <AssetViewerToolbar
          asset={asset}
          onBack={onBack}
          toolbarExtra={toolbarExtra}
          isOwner={isOwner}
          sharePermission={sharePermission}
          copyMutation={copyMutation}
          onDelete={() => setDeleteModalOpen(true)}
          onDownload={handleDownload}
          onCopyToMyAssets={handleCopyToMyAssets}
          onShare={() => setShareOpen(true)}
          sidebarOpen={sidebarOpen}
          onToggleSidebar={() => setSidebarOpen(!sidebarOpen)}
        />

        <KnowledgeBacklinks urn={`mcp:asset:${asset.id}`} onNavigate={onNavigate} />

        <AssetContentView
          asset={asset}
          content={content}
          contentUrl={contentUrl}
          canEditSource={canEditSource}
          viewingOldVersion={viewingOldVersion}
          viewMode={viewMode}
          onSetViewMode={setViewMode}
          versions={versions}
          onSelectVersion={onSelectVersion}
          selectedVersion={selectedVersion}
          isOwner={isOwner}
          sharePermission={sharePermission}
          revertMutation={revertMutation}
          onRevert={() => setRevertModalOpen(true)}
          onSaveContent={handleSaveContent}
          hasChanges={hasChanges}
          contentUpdateMutation={contentUpdateMutation}
          saveStatus={saveStatus}
          versionContentLoading={versionContentLoading}
          versionContent={versionContent}
          editedContent={editedContent}
          onSourceChange={(v) => { setEditedContent(v); setDirty(true); }}
        />
      </div>

      {/* Metadata sidebar */}
      {sidebarOpen && (
        <AssetMetadataSidebar
          asset={asset}
          editing={editing}
          editName={editName}
          editDesc={editDesc}
          editTags={editTags}
          onEditNameChange={setEditName}
          onEditDescChange={setEditDesc}
          onEditTagsChange={setEditTags}
          onStartEdit={startEdit}
          onSaveEdit={saveEdit}
          onCancelEdit={() => setEditing(false)}
          updateMutation={updateMutation}
          isOwner={isOwner}
          isSharedEditor={isSharedEditor}
          detailRows={detailRows}
          versions={versions}
          versionsLoading={versionsLoading}
        />
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

      <AssetViewerModals
        asset={asset}
        deleteModalOpen={deleteModalOpen}
        onDeleteClose={() => setDeleteModalOpen(false)}
        onConfirmDelete={confirmDelete}
        deleteMutation={deleteMutation}
        sharedSaveWarningOpen={sharedSaveWarningOpen}
        onSharedSaveWarningClose={() => setSharedSaveWarningOpen(false)}
        onSharedSaveWarningContinue={() => { setSharedSaveWarningOpen(false); setChangeSummaryOpen(true); }}
        contentUpdateMutation={contentUpdateMutation}
        changeSummaryOpen={changeSummaryOpen}
        onChangeSummaryClose={() => setChangeSummaryOpen(false)}
        changeSummary={changeSummary}
        onChangeSummaryChange={setChangeSummary}
        onChangeSummarySave={() => doSaveContent(changeSummary)}
        revertModalOpen={revertModalOpen}
        selectedVersion={selectedVersion}
        onRevertClose={() => setRevertModalOpen(false)}
        onConfirmRevert={handleConfirmRevert}
        revertMutation={revertMutation}
      />
    </div>
  );
}
