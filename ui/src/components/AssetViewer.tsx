import { useState, useCallback, useEffect } from "react";
import { FileQuestion } from "lucide-react";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { ShareDialog } from "@/components/ShareDialog";
import { Button } from "@/components/ui/button";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { useIdleGate } from "@/lib/idle";
import { isThumbnailSupported, thumbnailBehind, THUMBNAIL_SOURCE_LIMIT } from "@/lib/thumbnailSupport";
import { isEditableContent } from "@/components/renderers/registry";
import { type AssetViewerProps, type ViewMode } from "./assetviewer/types";
import { ThumbnailGeneratorWithInvalidation } from "./assetviewer/ThumbnailGeneratorWithInvalidation";
import { AssetViewerActions } from "./assetviewer/AssetViewerActions";
import { AssetContentView } from "./assetviewer/AssetContentView";
import { AssetMetadataSidebar } from "./assetviewer/AssetMetadataSidebar";
import {
  retentionModeFor,
  retentionUnchanged,
  retentionValue,
  type RetentionMode,
} from "./assetviewer/AssetRetentionField";
import { AssetViewerModals } from "./assetviewer/AssetViewerModals";
import { ViewerLayout } from "./viewer/ViewerLayout";

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
  sessionPath,
  resourcePath,
  assetPath,
  versions,
  versionsLoading,
  revertMutation,
  selectedVersion,
  onSelectVersion,
  versionContent,
  versionContentLoading,
}: AssetViewerProps) {
  const [shareOpen, setShareOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editName, setEditName] = useState("");
  const [editDesc, setEditDesc] = useState("");
  const [editTags, setEditTags] = useState("");
  const [editRetentionMode, setEditRetentionMode] = useState<RetentionMode>("default");
  const [editRetentionCustom, setEditRetentionCustom] = useState("");
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [sharedSaveWarningOpen, setSharedSaveWarningOpen] = useState(false);
  const [changeSummaryOpen, setChangeSummaryOpen] = useState(false);
  const [changeSummary, setChangeSummary] = useState("");
  const [revertModalOpen, setRevertModalOpen] = useState(false);

  const [viewMode, setViewMode] = useState<ViewMode>("preview");
  const [editedContent, setEditedContent] = useState<string>("");
  const [dirty, setDirty] = useState(false);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">("idle");

  // Capturing a thumbnail renders the asset a second time off-screen and
  // rasterizes it. Doing that while someone is reading the asset is what made
  // the detail page stop responding on a first visit (#1351), so it waits for
  // the browser to go idle with the tab in front, and is skipped entirely for
  // a document too large to render twice cheaply.
  //
  // Whether one is wanted is read off the asset row rather than tracked here:
  // a capture dated below the current version is one the reader is looking past
  // — the image on the card is of an older body — and a save the reader just
  // made moves the version, so the same comparison covers both (#1431).
  //
  // Only for a reader who could store the result: the capture endpoint takes an
  // upload from the asset's owner or an administrator, so on an asset shared
  // with the reader the whole pass would end in a refused PUT.
  const thumbnailWanted =
    isOwner &&
    typeof content === "string" &&
    content.length > 0 &&
    !!asset &&
    isThumbnailSupported(asset.content_type) &&
    asset.size_bytes <= THUMBNAIL_SOURCE_LIMIT &&
    thumbnailBehind(asset);
  const captureThumbnail = useIdleGate(thumbnailWanted);
  const isSharedEditor = !isOwner && sharePermission === "editor";

  const canEditSource =
    !!contentUpdateMutation && !!asset && isEditableContent(asset.content_type, asset.name);
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
      <EmptyState
        icon={FileQuestion}
        action={
          <Button variant="outline" size="sm" onClick={onBack}>
            Back
          </Button>
        }
      >
        <p className="font-medium">Asset not found</p>
      </EmptyState>
    );
  }

  function startEdit() {
    if (!asset) return;
    setEditName(asset.name);
    setEditDesc(asset.description ?? "");
    setEditTags(asset.tags.join(", "));
    setEditRetentionMode(retentionModeFor(asset.max_versions));
    // A custom count seeds the box; the other two modes leave the last typed
    // value alone rather than blanking a number the person may switch back to.
    if (asset.max_versions !== undefined && asset.max_versions > 0) {
      setEditRetentionCustom(String(asset.max_versions));
    }
    setEditing(true);
  }

  function saveEdit() {
    if (!asset) return;
    const tags = editTags
      .split(",")
      .map((t) => t.trim())
      .filter(Boolean);
    // Retention is sent only when it moved. The server reserves that field to
    // the owner and an admin, so an editor saving a rename must not restate a
    // setting they never touched and be refused for it.
    const retentionMoved = !retentionUnchanged(editRetentionMode, editRetentionCustom, asset.max_versions);
    updateMutation.mutate(
      {
        id: asset.id,
        name: editName,
        description: editDesc,
        tags,
        ...(retentionMoved
          ? { max_versions: retentionValue(editRetentionMode, editRetentionCustom) }
          : {}),
      },
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
    <>
      <ViewerLayout
        onBack={onBack}
        title={asset.name}
        actions={
          <AssetViewerActions
            toolbarExtra={toolbarExtra}
            isOwner={isOwner}
            sharePermission={sharePermission}
            copyMutation={copyMutation}
            onDelete={() => setDeleteModalOpen(true)}
            onDownload={handleDownload}
            onCopyToMyAssets={handleCopyToMyAssets}
            onShare={() => setShareOpen(true)}
          />
        }
        sidebar={
          <AssetMetadataSidebar
            asset={asset}
            editing={editing}
            editName={editName}
            editDesc={editDesc}
            editTags={editTags}
            editRetentionMode={editRetentionMode}
            editRetentionCustom={editRetentionCustom}
            canSetRetention={isOwner}
            onEditNameChange={setEditName}
            onEditDescChange={setEditDesc}
            onEditTagsChange={setEditTags}
            onEditRetentionModeChange={setEditRetentionMode}
            onEditRetentionCustomChange={setEditRetentionCustom}
            onStartEdit={startEdit}
            onSaveEdit={saveEdit}
            onCancelEdit={() => setEditing(false)}
            updateMutation={updateMutation}
            isOwner={isOwner}
            isSharedEditor={isSharedEditor}
            detailRows={detailRows}
            sessionPath={sessionPath}
            resourcePath={resourcePath}
            assetPath={assetPath}
            onNavigate={onNavigate}
            versions={versions}
            versionsLoading={versionsLoading}
          />
        }
      >
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
      </ViewerLayout>

      {/* Remounted per version so a save, or a rewrite arriving on a refetch,
          captures again rather than reusing the mounted capturer. The version
          stamped is the asset's current one: the asset and its content are
          refetched together, so a body older than the version it is dated to is
          possible only in the moment between the two responses, and the next
          write puts the asset back on the queue. */}
      {captureThumbnail && typeof content === "string" && (
        <ThumbnailGeneratorWithInvalidation
          key={asset.current_version}
          assetId={asset.id}
          content={content}
          contentType={asset.content_type}
          version={asset.current_version}
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
    </>
  );
}
