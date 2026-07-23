import { lazy, Suspense } from "react";
import { Download, Save, Eye, Code, RotateCcw, FileWarning } from "lucide-react";
import type { Asset, AssetVersion, SharePermission } from "@/api/portal/types";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { formatBytes } from "@/lib/format";
import { exceedsInlineLimit, rendersFromURL } from "@/components/renderers/registry";
import { useContentUrl } from "@/lib/useContentUrl";
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

/**
 * The size guard, per family.
 *
 * A family whose renderer streams from a URL (images, audio, video, PDF) has no
 * inline cutoff at all, and one whose renderer virtualizes (JSON, tabular) has
 * a far higher one than a block of text. The registry owns those limits. This
 * replaces the single 2 MB threshold that used to refuse every large asset
 * regardless of how its viewer works.
 */
function TooLarge({ asset, sizeBytes, contentUrl }: { asset: Asset; sizeBytes: number; contentUrl: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-4 py-20 text-center">
      <FileWarning className="h-12 w-12 text-muted-foreground" />
      <div>
        <p className="text-lg font-medium">Too large to preview</p>
        <p className="mt-1 text-sm text-muted-foreground">
          This file is {formatBytes(sizeBytes)}, past the inline preview limit for {asset.content_type}.
        </p>
      </div>
      <a
        href={contentUrl}
        download={asset.name}
        className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
      >
        <Download className="h-4 w-4" />
        Download
      </a>
    </div>
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

/** Preview/Source switch, shown only for families the editor supports. */
function ViewModeToggle({
  show,
  viewMode,
  onSetViewMode,
}: {
  show: boolean;
  viewMode: ViewMode;
  onSetViewMode: (mode: ViewMode) => void;
}) {
  if (!show) return null;

  const tab = (mode: ViewMode, label: string, Icon: typeof Eye, rounding: string) => (
    <button
      type="button"
      onClick={() => onSetViewMode(mode)}
      className={`flex items-center gap-1.5 px-3 py-1.5 ${rounding} transition-colors ${
        viewMode === mode ? "bg-accent font-medium" : "hover:bg-accent/50"
      }`}
    >
      <Icon className="h-3.5 w-3.5" />
      {label}
    </button>
  );

  return (
    <div className="inline-flex rounded-md border text-sm">
      {tab("preview", "Preview", Eye, "rounded-l-md")}
      {tab("source", "Source", Code, "rounded-r-md border-l")}
    </div>
  );
}

/** Version picker plus the revert action for an older version. */
function VersionControls({
  asset,
  versions,
  selectedVersion,
  onSelectVersion,
  viewingOldVersion,
  canRevert,
  onRevert,
}: {
  asset: Asset;
  versions?: AssetVersion[];
  selectedVersion?: number | null;
  onSelectVersion?: (v: number | null) => void;
  viewingOldVersion: boolean;
  canRevert: boolean;
  onRevert: () => void;
}) {
  if (!versions || versions.length === 0 || !onSelectVersion) return null;

  return (
    <>
      <select
        aria-label="Asset version"
        value={selectedVersion ?? asset.current_version}
        onChange={(e) => {
          const v = Number(e.target.value);
          onSelectVersion(v === asset.current_version ? null : v);
        }}
        className="rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      >
        {versions.map((v) => (
          <option key={v.version} value={v.version}>
            v{v.version}
            {v.version === asset.current_version ? " (current)" : ""}
          </option>
        ))}
      </select>
      {viewingOldVersion && canRevert && (
        <button
          type="button"
          onClick={onRevert}
          className="flex items-center gap-1.5 rounded-md border border-amber-500/30 px-3 py-1.5 text-sm font-medium text-amber-600 hover:bg-amber-50 dark:text-amber-400 dark:hover:bg-amber-950"
        >
          <RotateCcw className="h-3.5 w-3.5" />
          Revert
        </button>
      )}
    </>
  );
}

/** Save button and its result indicator, shown while editing source. */
function SaveControls({
  show,
  hasChanges,
  saveStatus,
  onSaveContent,
  pending,
}: {
  show: boolean;
  hasChanges: boolean;
  saveStatus: "idle" | "saved" | "error";
  onSaveContent: () => void;
  pending: boolean;
}) {
  if (!show) return null;

  return (
    <>
      <button
        type="button"
        onClick={onSaveContent}
        disabled={!hasChanges || pending}
        className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
      >
        <Save className="h-3.5 w-3.5" />
        {pending ? "Saving..." : "Save"}
      </button>
      {saveStatus === "saved" && <span className="text-xs text-green-600 dark:text-green-400">Saved</span>}
      {saveStatus === "error" && <span className="text-xs text-destructive">Save failed</span>}
    </>
  );
}
