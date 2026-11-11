import { lazy, Suspense, useState, type ReactNode } from "react";
import { DocActionsContext, type DocAction } from "@/components/renderers/docActions";
import { PhoneToolbar, type PhoneViewer } from "./PhoneToolbar";
import type { Asset, AssetVersion, SharePermission } from "@/api/portal/types";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { versionContentURL } from "@/lib/pdfExport";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { exceedsInlineLimit, readsByRange, rendersFromURL } from "@/components/renderers/registry";
import { useContentUrl } from "@/lib/useContentUrl";
import { ContentLoadError, SaveControls, TooLarge, VersionControls, ViewModeToggle } from "./contentControls";
import { pendingState, type PendingState } from "./pendingState";
import { ExpandButton, ExpandedView } from "./ExpandedView";
import { useExpandedView } from "./useExpandedView";
import { useIsMobile } from "@/hooks/useIsMobile";
import type { MutationLike, ViewMode } from "./types";

const SourceEditor = lazy(() =>
  import("@/components/SourceEditor").then((m) => ({ default: m.SourceEditor })),
);

interface AssetContentViewProps {
  asset: Asset;
  content: string | ArrayBuffer | undefined;
  /** Why the content read failed, when it did (#1874). */
  contentError?: unknown;
  onRetryContent?: () => void;
  contentUrl: string;
  canEditSource: boolean;
  viewingOldVersion: boolean;
  viewMode: ViewMode;
  onSetViewMode: (mode: ViewMode) => void;
  versions?: AssetVersion[];
  onSelectVersion?: (v: number | null) => void;
  selectedVersion?: number | null;
  isOwner: boolean;
  sharePermission?: SharePermission;
  revertMutation?: MutationLike<{ assetId: string; version: number }>;
  onRevert: () => void;
  onSaveContent: () => void;
  hasChanges: boolean;
  contentUpdateMutation?: MutationLike<{ id: string; content: string; changeSummary?: string }>;
  saveStatus: "idle" | "saved" | "error";
  versionContentLoading?: boolean;
  versionContent?: string;
  editedContent: string;
  onSourceChange: (v: string) => void;
  /**
   * What sits to the right of the version selector: the knowledge pages that
   * reference this asset, as a button (#1792).
   */
  afterVersion?: ReactNode;
  /**
   * The page's own actions for a phone's one-row toolbar (#2601). Given, a
   * phone gets that toolbar, with the document's controls in its More menu.
   */
  phone?: PhoneViewer;
}

export function AssetContentView({
  asset,
  content,
  contentError,
  onRetryContent,
  contentUrl,
  canEditSource,
  viewingOldVersion,
  viewMode,
  onSetViewMode,
  versions,
  onSelectVersion,
  selectedVersion,
  isOwner,
  sharePermission,
  revertMutation,
  onRevert,
  onSaveContent,
  hasChanges,
  contentUpdateMutation,
  saveStatus,
  versionContentLoading,
  versionContent,
  editedContent,
  onSourceChange,
  afterVersion,
  phone,
}: AssetContentViewProps) {
  // The end of the control row, where an HTML document's own controls
  // (Present, Overview, Export PDF) render rather than on a row of their own
  // (#1769). Held as state so the renderer re-renders once the row exists.
  const [controlsSlot, setControlsSlot] = useState<HTMLElement | null>(null);
  // On a phone the content opens edge to edge (#2390). A link with ?view=full
  // opens it that way at any width; the control is offered on a phone, where
  // the page around the content is what crowds it.
  const expand = useContentExpand(canEditSource, viewMode, viewingOldVersion);
  const isMobile = useIsMobile();
  const [docActions, setDocActions] = useState<DocAction[]>([]);
  const onPhone = isMobile && phone !== undefined;
  const { canRevert, showSave, savePending } = controlFlags({
    isOwner, sharePermission, revertMutation, viewMode, hasChanges, viewingOldVersion, contentUpdateMutation,
  });

  const toolbar = onPhone ? (
    <PhoneToolbar
      asset={asset}
      phone={phone}
      docActions={docActions}
      canEditSource={canEditSource}
      viewMode={viewMode}
      onSetViewMode={onSetViewMode}
      viewingOldVersion={viewingOldVersion}
      versions={versions}
      selectedVersion={selectedVersion}
      onSelectVersion={onSelectVersion}
      canRevert={canRevert}
      onRevert={onRevert}
      showSave={showSave}
      hasChanges={hasChanges}
      savePending={savePending}
      onSaveContent={onSaveContent}
      expandOffered={expand.offered}
      onExpand={expand.open}
      afterVersion={afterVersion}
    />
  ) : (
    <DesktopControls
      asset={asset}
      canEditSource={canEditSource}
      viewingOldVersion={viewingOldVersion}
      viewMode={viewMode}
      onSetViewMode={onSetViewMode}
      versions={versions}
      selectedVersion={selectedVersion}
      onSelectVersion={onSelectVersion}
      canRevert={canRevert}
      onRevert={onRevert}
      afterVersion={afterVersion}
      showSave={showSave}
      hasChanges={hasChanges}
      saveStatus={saveStatus}
      onSaveContent={onSaveContent}
      savePending={savePending}
      setControlsSlot={setControlsSlot}
      expand={expand}
    />
  );

  return (
    <DocActionsContext.Provider value={onPhone ? setDocActions : null}>
      {toolbar}

      {/* Content display */}
      <ExpandedView expanded={expand.expanded} title={asset.name} onClose={expand.close}>
        {viewingOldVersion ? (
          versionContentLoading ? (
            <LoadingIndicator />
          ) : (
            <VersionContent
              asset={asset}
              versions={versions}
              selectedVersion={selectedVersion}
              versionContent={versionContent}
              contentUrl={contentUrl}
              controlsSlot={controlsSlot}
            />
          )
        ) : (
          <CurrentContent
            asset={asset}
            content={content}
            contentError={contentError}
            onRetryContent={onRetryContent}
            contentUrl={contentUrl}
            canEditSource={canEditSource}
            viewMode={viewMode}
            editedContent={editedContent}
            hasChanges={hasChanges}
            onSourceChange={onSourceChange}
            controlsSlot={controlsSlot}
          />
        )}
      </ExpandedView>
    </DocActionsContext.Provider>
  );
}

/** controlFlags are what the controls offer: Revert to an owner or an editor
 * where reverting is wired, Save in Source or once there are edits (but not on
 * an older version), and whether a save is under way. */
function controlFlags(p: {
  isOwner: boolean;
  sharePermission?: SharePermission;
  revertMutation?: unknown;
  viewMode: ViewMode;
  hasChanges: boolean;
  viewingOldVersion: boolean;
  contentUpdateMutation?: { isPending?: boolean };
}) {
  return {
    canRevert: (p.isOwner || p.sharePermission === "editor") && !!p.revertMutation,
    // In Preview too once there are edits: a scene is edited where it is drawn
    // (#2306), and an edit made in Source survives the toggle.
    showSave: (p.viewMode === "source" || p.hasChanges) && !p.viewingOldVersion,
    savePending: !!p.contentUpdateMutation?.isPending,
  };
}

/** Whether Expand is offered and showing. The source editor is never
 * expanded: it is a form with its own Save, not content to read. */
function useContentExpand(canEditSource: boolean, viewMode: ViewMode, viewingOldVersion: boolean) {
  const editing = canEditSource && viewMode === "source" && !viewingOldVersion;
  const isMobile = useIsMobile();
  const view = useExpandedView();
  return {
    offered: isMobile && !editing,
    expanded: view.expanded && !editing,
    open: view.expand,
    close: view.close,
  };
}

function VersionContent({
  asset,
  versions,
  selectedVersion,
  versionContent,
  contentUrl,
  controlsSlot,
}: {
  asset: Asset;
  versions?: AssetVersion[];
  selectedVersion?: number | null;
  versionContent?: string;
  contentUrl: string;
  controlsSlot: HTMLElement | null;
}) {
  const version = versions?.find((v) => v.version === selectedVersion);
  const sizeBytes = version?.size_bytes ?? 0;
  const contentType = version?.content_type || asset.content_type;
  // The version shown, not the current one: a download or an export of v2
  // is v2 (#1983).
  const versionUrl = versionContentURL(contentUrl, selectedVersion);

  if (versionContent === undefined && exceedsInlineLimit(contentType, sizeBytes, asset.name)) {
    return <TooLarge asset={asset} sizeBytes={sizeBytes} contentUrl={versionUrl} />;
  }

  return (
    <ContentRenderer
      contentType={contentType}
      content={versionContent}
      fileName={asset.name}
      contentUrl={versionUrl}
      sizeBytes={sizeBytes}
      controlsSlot={controlsSlot}
    />
  );
}

function CurrentContent({
  asset,
  content,
  contentError,
  onRetryContent,
  contentUrl,
  canEditSource,
  viewMode,
  editedContent,
  hasChanges,
  onSourceChange,
  controlsSlot,
}: {
  asset: Asset;
  content: string | ArrayBuffer | undefined;
  contentError?: unknown;
  onRetryContent?: () => void;
  contentUrl: string;
  canEditSource: boolean;
  viewMode: ViewMode;
  editedContent: string;
  hasChanges: boolean;
  onSourceChange: (v: string) => void;
  controlsSlot: HTMLElement | null;
}) {
  // Binary families never load content into the page: their renderers point an
  // element at the content endpoint, so there is nothing to wait for.
  const fromURL = rendersFromURL(asset.content_type, asset.name);
  // A family that reads the endpoint by range is handed the endpoint: fetching
  // the whole object into a blob first is what reading by range avoids.
  const media = useContentUrl(contentUrl, fromURL && !readsByRange(asset.content_type, asset.name));

  // The read that failed is the one this family renders from: the media URL
  // for a URL family, the text otherwise.
  const failure = fromURL
    ? { error: media.error, retry: media.retry }
    : { error: contentError, retry: onRetryContent };
  const pending = pendingState({
    asset,
    content,
    fromURL,
    mediaLoading: media.loading,
    contentFailed: contentError != null,
    mediaFailed: media.error != null,
  });
  if (pending !== "ready") {
    return <PendingView pending={pending} asset={asset} contentUrl={contentUrl} {...failure} />;
  }

  const showEditor = canEditSource && viewMode === "source";

  return (
    <>
      {canEditSource && (
        <div style={{ display: showEditor ? undefined : "none" }}>
          <Suspense fallback={<LoadingIndicator />}>
            <SourceEditor
              content={editedContent}
              contentType={asset.content_type}
              fileName={asset.name}
              onChange={onSourceChange}
            />
          </Suspense>
        </div>
      )}
      {!showEditor && (
        <ContentRenderer
          contentType={asset.content_type}
          content={hasChanges ? editedContent : asText(content)}
          fileName={asset.name}
          // Unsaved edits are not what the stored document's routes serve, so
          // nothing is exported from them until they are saved (#1983).
          contentUrl={storedContentUrl(hasChanges, media.src || contentUrl)}
          sizeBytes={asset.size_bytes}
          controlsSlot={controlsSlot}
          onEdit={editInPlace(canEditSource, onSourceChange)}
        />
      )}
    </>
  );
}

/**
 * What a family edited where it is drawn (an Excalidraw scene, #2306) hands
 * its edits to: the page's draft, whose Save writes the version, for a reader
 * who may edit, and nothing otherwise.
 */
function editInPlace(canEditSource: boolean, onSourceChange: (v: string) => void): ((v: string) => void) | undefined {
  return canEditSource ? onSourceChange : undefined;
}

/** The URL the stored document is read from, or none while there are unsaved edits. */
function storedContentUrl(hasChanges: boolean, url: string): string | undefined {
  return hasChanges ? undefined : url;
}

/** What the viewer shows in place of content it cannot render yet, or at all. */
function PendingView({
  pending,
  asset,
  contentUrl,
  error,
  retry,
}: {
  pending: Exclude<PendingState, "ready">;
  asset: Asset;
  contentUrl: string;
  error: unknown;
  retry?: () => void;
}) {
  switch (pending) {
    case "too-large":
      return <TooLarge asset={asset} sizeBytes={asset.size_bytes} contentUrl={contentUrl} />;
    case "failed":
      return <ContentLoadError asset={asset} error={error} contentUrl={contentUrl} onRetry={retry} />;
    default:
      return <LoadingIndicator />;
  }
}

/** Content the renderers can take as text; an ArrayBuffer body is not one. */
function asText(content: string | ArrayBuffer | undefined): string | undefined {
  return typeof content === "string" ? content : undefined;
}

/** DesktopControls is the control row above the content on a wide screen:
 * Preview/Source, the version picker, Save, and the document's own controls
 * in the slot at its end. */
function DesktopControls({
  asset,
  canEditSource,
  viewingOldVersion,
  viewMode,
  onSetViewMode,
  versions,
  selectedVersion,
  onSelectVersion,
  canRevert,
  onRevert,
  afterVersion,
  showSave,
  hasChanges,
  saveStatus,
  onSaveContent,
  savePending,
  setControlsSlot,
  expand,
}: {
  asset: Asset;
  canEditSource: boolean;
  viewingOldVersion: boolean;
  viewMode: ViewMode;
  onSetViewMode: (mode: ViewMode) => void;
  versions?: AssetVersion[];
  selectedVersion?: number | null;
  onSelectVersion?: (v: number | null) => void;
  canRevert: boolean;
  onRevert: () => void;
  afterVersion?: ReactNode;
  showSave: boolean;
  hasChanges: boolean;
  saveStatus: "idle" | "saved" | "error";
  onSaveContent: () => void;
  savePending: boolean;
  setControlsSlot: (el: HTMLElement | null) => void;
  expand: { offered: boolean; open: () => void };
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <ViewModeToggle
        show={canEditSource && !viewingOldVersion}
        viewMode={viewMode}
        onSetViewMode={onSetViewMode}
      />
      <VersionControls
        asset={asset}
        versions={versions}
        selectedVersion={selectedVersion}
        onSelectVersion={onSelectVersion}
        viewingOldVersion={viewingOldVersion}
        canRevert={canRevert}
        onRevert={onRevert}
      />
      {afterVersion}
      <SaveControls
        // In Preview too once there are edits: a scene is edited where it is
        // drawn (#2306), and an edit made in Source survives the toggle.
        show={showSave}
        hasChanges={hasChanges}
        saveStatus={saveStatus}
        onSaveContent={onSaveContent}
        pending={savePending}
      />
      {viewingOldVersion && (
        <span className="text-xs text-muted-foreground">Viewing v{selectedVersion} (read-only)</span>
      )}
      <div ref={setControlsSlot} data-testid="content-controls" className="ml-auto flex flex-wrap items-center gap-2" />
      <ExpandButton offered={expand.offered} onClick={expand.open} />
    </div>
  );
}
