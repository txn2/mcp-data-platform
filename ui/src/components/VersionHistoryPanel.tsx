import { useState } from "react";
import { History, ChevronDown, ChevronUp } from "lucide-react";
import type { AssetVersion } from "@/api/portal/types";
import { formatBytes } from "@/lib/format";

interface VersionHistoryPanelProps {
  versions: AssetVersion[];
  currentVersion: number;
  isLoading: boolean;
}

export function VersionHistoryPanel({
  versions,
  currentVersion,
  isLoading,
}: VersionHistoryPanelProps) {
  const [expanded, setExpanded] = useState(false);

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
