import { lazy, Suspense, useState, type ReactNode } from "react";
import type { Asset, AssetVersion, SharePermission } from "@/api/portal/types";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { exceedsInlineLimit, readsByRange, rendersFromURL } from "@/components/renderers/registry";
import { useContentUrl } from "@/lib/useContentUrl";
import { ContentLoadError, SaveControls, TooLarge, VersionControls, ViewModeToggle } from "./contentControls";
import { pendingState, type PendingState } from "./pendingState";
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
}: AssetContentViewProps) {
  // The end of the control row, where an HTML document's own controls
  // (Present, Overview, Export PDF) render rather than on a row of their own
  // (#1769). Held as state so the renderer re-renders once the row exists.
  const [controlsSlot, setControlsSlot] = useState<HTMLElement | null>(null);

  return (
    <>
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
          canRevert={(isOwner || sharePermission === "editor") && !!revertMutation}
          onRevert={onRevert}
        />
        {afterVersion}
        <SaveControls
          show={viewMode === "source" && !viewingOldVersion}
          hasChanges={hasChanges}
          saveStatus={saveStatus}
          onSaveContent={onSaveContent}
          pending={!!contentUpdateMutation?.isPending}
        />
        {viewingOldVersion && (
          <span className="text-xs text-muted-foreground">Viewing v{selectedVersion} (read-only)</span>
        )}
        <div ref={setControlsSlot} data-testid="content-controls" className="ml-auto flex flex-wrap items-center gap-2" />
      </div>

      {/* Content display */}
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
    </>
  );
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

  if (versionContent === undefined && exceedsInlineLimit(contentType, sizeBytes, asset.name)) {
    return <TooLarge asset={asset} sizeBytes={sizeBytes} contentUrl={contentUrl} />;
  }

  return (
    <ContentRenderer
      contentType={contentType}
      content={versionContent}
      fileName={asset.name}
      contentUrl={contentUrl}
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
          contentUrl={media.src || contentUrl}
          sizeBytes={asset.size_bytes}
          controlsSlot={controlsSlot}
        />
      )}
    </>
  );
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
