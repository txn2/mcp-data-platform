import { lazy, Suspense } from "react";
import { Download, Save, Eye, Code, RotateCcw, FileWarning } from "lucide-react";
import type { Asset, AssetVersion, SharePermission } from "@/api/portal/types";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { formatBytes } from "@/lib/format";
import { LARGE_ASSET_THRESHOLD } from "@/api/portal/hooks";
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
      {/* View mode toggle + version dropdown + save button */}
      <div className="flex items-center gap-2">
        {canEditSource && !viewingOldVersion && (
          <div className="inline-flex rounded-md border text-sm">
            <button
              type="button"
              onClick={() => onSetViewMode("preview")}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-l-md transition-colors ${viewMode === "preview" ? "bg-accent font-medium" : "hover:bg-accent/50"}`}
            >
              <Eye className="h-3.5 w-3.5" />
              Preview
            </button>
            <button
              type="button"
              onClick={() => onSetViewMode("source")}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-r-md border-l transition-colors ${viewMode === "source" ? "bg-accent font-medium" : "hover:bg-accent/50"}`}
            >
              <Code className="h-3.5 w-3.5" />
              Source
            </button>
          </div>
        )}

        {/* Version dropdown */}
        {versions && versions.length > 0 && onSelectVersion && (
          <>
            <select
              value={selectedVersion ?? asset.current_version}
              onChange={(e) => {
                const v = Number(e.target.value);
                onSelectVersion(v === asset.current_version ? null : v);
              }}
              className="rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
            >
              {versions.map((v) => (
                <option key={v.version} value={v.version}>
                  v{v.version}{v.version === asset.current_version ? " (current)" : ""}
                </option>
              ))}
            </select>
            {viewingOldVersion && (isOwner || sharePermission === "editor") && revertMutation && (
              <button
                type="button"
                onClick={onRevert}
                className="flex items-center gap-1.5 rounded-md border border-amber-500/30 px-3 py-1.5 text-sm font-medium text-amber-600 dark:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-950"
              >
                <RotateCcw className="h-3.5 w-3.5" />
                Revert
              </button>
            )}
          </>
        )}

        {viewMode === "source" && !viewingOldVersion && (
          <>
            <button
              type="button"
              onClick={onSaveContent}
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

        {viewingOldVersion && (
          <span className="text-xs text-muted-foreground">Viewing v{selectedVersion} (read-only)</span>
        )}
      </div>

      {/* Content display */}
      {viewingOldVersion ? (
        versionContentLoading ? (
          <LoadingIndicator />
        ) : (() => {
          const versionSize = versions?.find(v => v.version === selectedVersion)?.size_bytes ?? 0;
          return versionSize > LARGE_ASSET_THRESHOLD && !versionContent ? (
            <div className="flex flex-col items-center justify-center gap-4 py-20 text-center">
              <FileWarning className="h-12 w-12 text-muted-foreground" />
              <div>
                <p className="text-lg font-medium">Version too large to preview</p>
                <p className="text-sm text-muted-foreground mt-1">
                  This version is {formatBytes(versionSize)} which exceeds the {formatBytes(LARGE_ASSET_THRESHOLD)} preview limit.
                </p>
              </div>
              <a
                href={contentUrl}
                download={asset.name}
                className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
              >
                <Download className="h-4 w-4" />
                Download Current Version
              </a>
            </div>
          ) : (
            <ContentRenderer contentType={asset.content_type} content={versionContent ?? ""} fileName={asset.name} />
          );
        })()
      ) : asset.size_bytes > LARGE_ASSET_THRESHOLD && content === undefined ? (
        <div className="flex flex-col items-center justify-center gap-4 py-20 text-center">
          <FileWarning className="h-12 w-12 text-muted-foreground" />
          <div>
            <p className="text-lg font-medium">Asset too large to preview</p>
            <p className="text-sm text-muted-foreground mt-1">
              This file is {formatBytes(asset.size_bytes)} which exceeds the {formatBytes(LARGE_ASSET_THRESHOLD)} preview limit.
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
      ) : content !== undefined ? (
        <>
          {canEditSource && (
            <div style={{ display: viewMode === "source" ? undefined : "none" }}>
              <Suspense fallback={<LoadingIndicator />}>
                <SourceEditor
                  content={editedContent}
                  contentType={asset.content_type}
                  onChange={onSourceChange}
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
    </>
  );
}
