import { lazy, Suspense } from "react";
import type { Asset, AssetVersion, SharePermission } from "@/api/portal/types";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { exceedsInlineLimit, rendersFromURL } from "@/components/renderers/registry";
import { useContentUrl } from "@/lib/useContentUrl";
import { SaveControls, TooLarge, VersionControls, ViewModeToggle } from "./contentControls";
import type { MutationLike, ViewMode } from "./types";

const SourceEditor = lazy(() =>
  import("@/components/SourceEditor").then((m) => ({ default: m.SourceEditor })),
);

interface AssetContentViewProps {
  asset: Asset;
  content: string | ArrayBuffer | undefined;
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
}

export function AssetContentView({
  asset,
  content,
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
}: AssetContentViewProps) {
  return (
    <>
      <div className="flex items-center gap-2">
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
          />
        )
      ) : (
        <CurrentContent
          asset={asset}
          content={content}
          contentUrl={contentUrl}
          canEditSource={canEditSource}
          viewMode={viewMode}
          editedContent={editedContent}
          hasChanges={hasChanges}
          onSourceChange={onSourceChange}
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
}: {
  asset: Asset;
  versions?: AssetVersion[];
  selectedVersion?: number | null;
  versionContent?: string;
  contentUrl: string;
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
    />
  );
}

function CurrentContent({
  asset,
  content,
  contentUrl,
  canEditSource,
  viewMode,
  editedContent,
  hasChanges,
  onSourceChange,
}: {
  asset: Asset;
  content: string | ArrayBuffer | undefined;
  contentUrl: string;
  canEditSource: boolean;
  viewMode: ViewMode;
  editedContent: string;
  hasChanges: boolean;
  onSourceChange: (v: string) => void;
}) {
  // Binary families never load content into the page: their renderers point an
  // element at the content endpoint, so there is nothing to wait for.
  const fromURL = rendersFromURL(asset.content_type, asset.name);
  const media = useContentUrl(contentUrl, fromURL);

  const pending = pendingState({ asset, content, fromURL, mediaLoading: media.loading });
  if (pending === "too-large") {
    return <TooLarge asset={asset} sizeBytes={asset.size_bytes} contentUrl={contentUrl} />;
  }
  if (pending === "loading") {
    return <LoadingIndicator />;
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
        />
      )}
    </>
  );
}

/**
 * What, if anything, stands between the viewer and rendering: the asset is past
 * its family's inline limit, its content has not arrived, or its media URL is
 * still being fetched.
 */
function pendingState({
  asset,
  content,
  fromURL,
  mediaLoading,
}: {
  asset: Asset;
  content: string | ArrayBuffer | undefined;
  fromURL: boolean;
  mediaLoading: boolean;
}): "too-large" | "loading" | "ready" {
  if (fromURL) {
    return mediaLoading ? "loading" : "ready";
  }
  if (content !== undefined) {
    return "ready";
  }
  return exceedsInlineLimit(asset.content_type, asset.size_bytes, asset.name) ? "too-large" : "loading";
}

/** Content the renderers can take as text; an ArrayBuffer body is not one. */
function asText(content: string | ArrayBuffer | undefined): string | undefined {
  return typeof content === "string" ? content : undefined;
}
