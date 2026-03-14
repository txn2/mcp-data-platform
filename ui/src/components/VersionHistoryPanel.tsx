import { useState } from "react";
import { History, RotateCcw, ChevronDown, ChevronUp, Eye } from "lucide-react";
import type { AssetVersion } from "@/api/portal/types";
import { formatBytes } from "@/lib/format";

interface VersionHistoryPanelProps {
  versions: AssetVersion[];
  currentVersion: number;
  isLoading: boolean;
  canEdit: boolean;
  onRevert: (version: number) => void;
  isReverting: boolean;
  onPreview?: (version: number) => void;
}

export function VersionHistoryPanel({
  versions,
  currentVersion,
  isLoading,
  canEdit,
  onRevert,
  isReverting,
  onPreview,
}: VersionHistoryPanelProps) {
  const [expanded, setExpanded] = useState(false);
  const [confirmVersion, setConfirmVersion] = useState<number | null>(null);

  if (isLoading) {
    return (
      <div className="space-y-2">
        <h3 className="text-sm font-medium flex items-center gap-1.5">
          <History className="h-3.5 w-3.5" /> Versions
        </h3>
        <p className="text-xs text-muted-foreground">Loading...</p>
      </div>
    );
  }

  if (versions.length === 0) {
    return null;
  }

  const preview = expanded ? versions : versions.slice(0, 3);

  return (
    <div className="space-y-2">
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        className="flex w-full items-center justify-between text-sm font-medium hover:text-foreground/80"
      >
        <span className="flex items-center gap-1.5">
          <History className="h-3.5 w-3.5" /> Versions ({versions.length})
        </span>
        {versions.length > 3 && (
          expanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />
        )}
      </button>

      <div className="space-y-1.5">
        {preview.map((v) => (
          <div
            key={v.id}
            className={`rounded-md border px-2.5 py-1.5 text-xs ${
              v.version === currentVersion
                ? "border-primary/40 bg-primary/5"
                : "border-border"
            }`}
          >
            <div className="flex items-center justify-between">
              <span className="font-medium">
                v{v.version}
                {v.version === currentVersion && (
                  <span className="ml-1.5 text-[10px] text-primary font-normal">(current)</span>
                )}
              </span>
              <span className="text-muted-foreground">
                {formatBytes(v.size_bytes)}
              </span>
            </div>
            <div className="text-muted-foreground mt-0.5">
              {new Date(v.created_at).toLocaleString()}
            </div>
            {v.change_summary && (
              <div className="text-muted-foreground mt-0.5 italic">
                {v.change_summary}
              </div>
            )}
            {v.created_by && (
              <div className="text-muted-foreground mt-0.5">
                by {v.created_by}
              </div>
            )}

            {v.version !== currentVersion && (
              <div className="flex gap-1.5 mt-1.5">
                {onPreview && (
                  <button
                    type="button"
                    onClick={() => onPreview(v.version)}
                    className="flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] hover:bg-accent"
                    title="Preview this version"
                  >
                    <Eye className="h-3 w-3" /> Preview
                  </button>
                )}
                {canEdit && (
                  confirmVersion === v.version ? (
                    <div className="flex items-center gap-1">
                      <span className="text-[10px] text-muted-foreground">Revert?</span>
                      <button
                        type="button"
                        onClick={() => {
                          onRevert(v.version);
                          setConfirmVersion(null);
                        }}
                        disabled={isReverting}
                        className="rounded bg-destructive px-1.5 py-0.5 text-[10px] text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
                      >
                        Yes
                      </button>
                      <button
                        type="button"
                        onClick={() => setConfirmVersion(null)}
                        className="rounded bg-secondary px-1.5 py-0.5 text-[10px] text-secondary-foreground hover:bg-secondary/80"
                      >
                        No
                      </button>
                    </div>
                  ) : (
                    <button
                      type="button"
                      onClick={() => setConfirmVersion(v.version)}
                      className="flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] hover:bg-accent"
                      title="Revert to this version"
                    >
                      <RotateCcw className="h-3 w-3" /> Revert
                    </button>
                  )
                )}
              </div>
            )}
          </div>
        ))}
      </div>

      {!expanded && versions.length > 3 && (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className="text-xs text-primary hover:underline"
        >
          Show all {versions.length} versions
        </button>
      )}
    </div>
  );
}
