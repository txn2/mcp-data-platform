import { useState, useCallback, useEffect, type ReactNode } from "react";
import { FileQuestion, Share2 } from "lucide-react";
import { KnowledgeBacklinksButton } from "@/components/knowledge/KnowledgeBacklinksButton";
import { EmptyState } from "@/components/patterns/EmptyState";
import { ShareDialog } from "@/components/ShareDialog";
import { Button } from "@/components/ui/button";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { isEditableContent } from "@/components/renderers/registry";
import type { Asset } from "@/api/portal/types";
import { type AssetViewerProps, type ViewMode } from "./assetviewer/types";
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
import { AddToCollection, useAddToCollectionAction } from "./assetviewer/AddToCollectionDialog";
import { ViewerLayout } from "./viewer/ViewerLayout";
import { IncompleteBadge } from "@/components/IncompleteBadge";
import { isTruncated } from "@/lib/truncation";
import { useAuthStore } from "@/stores/auth";
import { usePortalAccess } from "@/hooks/usePortalAccess";
import { useStandaloneDownload } from "@/components/StandaloneDownload";
import { authHeaders } from "@/api/portal/client";
import { useIsMobile } from "@/hooks/useIsMobile";
import type { PhoneViewer } from "./assetviewer/PhoneToolbar";
import type { SharePermission } from "@/api/portal/types";

export type { AssetViewerProps } from "./assetviewer/types";

export function AssetViewer({
  asset,
  content,
  contentError,
  onRetryContent,
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
  phoneToolbarExtra,
  collectable,
  detailRows,
  sessionPath,
  resourcePath,
  assetPath,
  scriptPath,
  assetApiBase,
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
  const [detailsOpen, setDetailsOpen] = useState(false);
  const isMobile = useIsMobile();

  const [viewMode, setViewMode] = useState<ViewMode>("preview");
  const [editedContent, setEditedContent] = useState<string>("");
  const [dirty, setDirty] = useState(false);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">("idle");

  const isSharedEditor = !isOwner && sharePermission === "editor";
  const canShare = useAuthStore((s) => s.canShare());
  const portal = usePortalAccess();

  const addToCollection = useAddToCollectionAction(asset?.id, !!collectable);

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

  // An HTML or JSX asset downloads as one self-contained file (#2530);
  // anything else as the content route serves it.
  const standalone = useStandaloneDownload(authHeaders);
  const startDownload = standalone.start;
  const handleDownload = useCallback(() => {
    if (!asset) return;
    void startDownload(contentUrl, asset.name, asset.content_type);
  }, [asset, contentUrl, startDownload]);

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
      {standalone.dialog}
      <ViewerLayout
        onBack={onBack}
        title={<AssetTitle asset={asset} />}
        phoneHeader
        sidebarOpen={detailsOpen}
        onSidebarOpenChange={setDetailsOpen}
        subtitle={phoneSubtitle(isMobile, {
          asset,
          isOwner,
          sharePermission,
          hasChanges,
          saveStatus,
          viewingVersion: viewingOldVersion ? selectedVersion : null,
        })}
        actions={
          <AssetViewerActions
            toolbarExtra={toolbarExtra}
            isOwner={isOwner}
            canShare={canShare}
            sharePermission={sharePermission}
            // Saving a copy creates an asset, which a persona that only reads
            // what is shared with it does not do (#2225).
            copyMutation={portal.assets === "own" ? copyMutation : undefined}
            onDelete={() => setDeleteModalOpen(true)}
            onDownload={handleDownload}
            downloadProgress={standalone.progress}
            onCopyToMyAssets={handleCopyToMyAssets}
            onShare={() => setShareOpen(true)}
            onAddToCollection={addToCollection.onOpen}
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
            scriptPath={scriptPath}
            assetApiBase={assetApiBase}
            onNavigate={onNavigate}
            versions={versions}
            versionsLoading={versionsLoading}
          />
        }
      >
        <AssetContentView
          asset={asset}
          content={content}
          contentError={contentError}
          onRetryContent={onRetryContent}
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
          afterVersion={<KnowledgeBacklinksButton urn={`mcp:asset:${asset.id}`} onNavigate={onNavigate} compact={isMobile} />}
          phone={phoneViewer({
            isOwner,
            canShare,
            ownsAssets: portal.assets === "own",
            copyMutation,
            extra: phoneToolbarExtra,
            detailsOpen,
            onShare: () => setShareOpen(true),
            onToggleDetails: () => setDetailsOpen((open) => !open),
            onDownload: handleDownload,
            downloadBusy: standalone.busy,
            onAddToCollection: addToCollection.onOpen,
            onCopyToMyAssets: handleCopyToMyAssets,
            onDelete: () => setDeleteModalOpen(true),
          })}
        />
      </ViewerLayout>

      <ShareDialog assetId={asset.id} open={shareOpen} onOpenChange={setShareOpen} />
      <AddToCollection action={addToCollection} assetId={asset.id} onNavigate={onNavigate} />

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

/** AssetTitle is the asset's name, with the Incomplete mark beside it for a cut
 * export: marked where the file is opened, not only in the details panel,
 * which starts closed (#2057). */
function AssetTitle({ asset }: { asset: Pick<Asset, "name" | "tags"> }) {
  return (
    <>
      {asset.name}
      {isTruncated(asset.tags) && <IncompleteBadge className="ml-2 align-middle" />}
    </>
  );
}

/** phoneSubtitle is the quiet line under the title on a phone, and none on a
 * wider screen, whose header has no line there. */
function phoneSubtitle(phone: boolean, props: Parameters<typeof PhoneSubtitle>[0]): ReactNode {
  return phone ? <PhoneSubtitle {...props} /> : undefined;
}

/** PhoneSubtitle is the quiet line under a phone's title (#2601): whether the
 * asset is shared with the reader and as what, when it was updated, and that
 * there are unsaved edits or that an older version is open, read-only. */
function PhoneSubtitle({
  asset,
  isOwner,
  sharePermission,
  hasChanges,
  saveStatus,
  viewingVersion,
}: {
  asset: Pick<Asset, "updated_at" | "current_version">;
  isOwner: boolean;
  sharePermission?: SharePermission;
  hasChanges: boolean;
  saveStatus: "idle" | "saved" | "error";
  viewingVersion: number | null | undefined;
}) {
  const parts: ReactNode[] = [];
  if (!isOwner) {
    parts.push(
      <span key="share" className="inline-flex items-center gap-1.5 text-amber-700 dark:text-amber-400">
        <span aria-hidden className="size-1.5 rounded-full bg-current" />
        Shared with you · {sharePermission === "editor" ? "Editor" : "Viewer"}
      </span>,
    );
  }
  const updated = updatedOn(asset.updated_at);
  if (updated) parts.push(<span key="updated">Updated {updated}</span>);
  if (viewingVersion) {
    parts.push(<span key="version">Viewing v{viewingVersion} of {asset.current_version}, read-only</span>);
  } else if (hasChanges) {
    parts.push(<span key="dirty" className="text-amber-700 dark:text-amber-400">Unsaved changes</span>);
  } else if (saveStatus === "saved") {
    parts.push(<span key="saved">Saved</span>);
  } else if (saveStatus === "error") {
    parts.push(<span key="failed" className="text-destructive">Save failed</span>);
  }
  return (
    <span className="flex flex-wrap items-center gap-x-1.5" data-testid="phone-subtitle">
      {parts.map((part, i) => (
        <span key={i} className="inline-flex items-center gap-1.5">
          {i > 0 && <span aria-hidden>·</span>}
          {part}
        </span>
      ))}
    </span>
  );
}

/** updatedOn is a short date ("Nov 10"), with the year when it is not this one. */
function updatedOn(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  const thisYear = at.getFullYear() === new Date().getFullYear();
  return at.toLocaleDateString(undefined, { month: "short", day: "numeric", year: thisYear ? undefined : "numeric" });
}

/** phoneViewer is what a phone's toolbar gets from the page (#2601): Share
 * for an owner whose persona shares, and the page's Feedback, on the row; the
 * rest in More, each only where the desktop offers it. */
function phoneViewer(p: {
  isOwner: boolean;
  canShare: boolean;
  ownsAssets: boolean;
  copyMutation?: { isPending?: boolean };
  extra?: ReactNode;
  detailsOpen: boolean;
  onShare: () => void;
  onToggleDetails: () => void;
  onDownload?: () => void;
  downloadBusy?: boolean;
  onAddToCollection?: () => void;
  onCopyToMyAssets: () => void;
  onDelete: () => void;
}): PhoneViewer {
  const copies = p.ownsAssets && !!p.copyMutation;
  return {
    actions: (
      <>
        {p.isOwner && p.canShare && (
          <Button variant="outline" size="icon-sm" onClick={p.onShare} aria-label="Share" title="Share">
            <Share2 />
          </Button>
        )}
        {p.extra}
      </>
    ),
    more: {
      detailsOpen: p.detailsOpen,
      onToggleDetails: p.onToggleDetails,
      onDownload: p.onDownload,
      downloadBusy: p.downloadBusy,
      onAddToCollection: p.onAddToCollection,
      onCopyToMyAssets: copies ? p.onCopyToMyAssets : undefined,
      copyPending: !!p.copyMutation?.isPending,
      onDelete: p.isOwner ? p.onDelete : undefined,
    },
  };
}
