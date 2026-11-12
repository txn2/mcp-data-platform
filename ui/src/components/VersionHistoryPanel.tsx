import { type ReactNode, useState } from "react";
import { History, ChevronDown, ChevronUp } from "lucide-react";
import type { AssetVersion } from "@/api/portal/types";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { truncationLabel, truncationOf } from "@/lib/truncation";
import { type ComparePair, VersionCompareDialog, comparable, completeVersion } from "./VersionCompareDialog";

/** CompareTarget is the asset whose versions can be compared (#2600). */
export interface CompareTarget {
  assetId: string;
  assetName: string;
  contentType: string;
}

interface VersionHistoryPanelProps {
  versions: AssetVersion[];
  currentVersion: number;
  isLoading: boolean;
  /** compare turns on comparing versions; left out, or for a binary asset, the list shows none. */
  compare?: CompareTarget;
}

export function VersionHistoryPanel({
  versions,
  currentVersion,
  isLoading,
  compare,
}: VersionHistoryPanelProps) {
  const [expanded, setExpanded] = useState(false);
  const [pair, setPair] = useState<ComparePair | null>(null);
  const cmp = compareState(versions, currentVersion, compare);

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
          <VersionRow key={v.id} version={v} currentVersion={currentVersion}>
            {cmp.canCompare && (
              <CompareLink
                version={v}
                currentVersion={currentVersion}
                previous={cmp.previous}
                currentComplete={cmp.currentComplete}
                onCompare={setPair}
              />
            )}
          </VersionRow>
        ))}
      </div>

      {compare && cmp.canCompare && (
        <CompareFooter
          compare={compare}
          versions={versions}
          currentVersion={currentVersion}
          twoVersions={cmp.twoVersions}
          pair={pair}
          onPair={setPair}
        />
      )}

      {!expanded && versions.length > 3 && (
        <Button type="button" variant="link" size="xs" onClick={() => setExpanded(true)} className="px-0">
          Show all {versions.length} versions
        </Button>
      )}
    </div>
  );
}

// CompareLink is a version's compare control: what changed in the current
// version since the one before it, "Compare with current" on an older one, and
// on a version that holds part of its content why it cannot be compared.
function CompareLink({
  version,
  currentVersion,
  previous,
  currentComplete,
  onCompare,
}: {
  version: AssetVersion;
  currentVersion: number;
  previous: number;
  currentComplete: boolean;
  onCompare: (pair: ComparePair) => void;
}) {
  if (!completeVersion(version)) {
    return <div className="mt-0.5 text-muted-foreground">Cannot be compared: this version holds only part of its content.</div>;
  }
  if (version.version === currentVersion) {
    if (previous === 0) return null;
    return (
      <Button type="button" variant="link" size="xs" className="h-auto px-0" onClick={() => onCompare({ from: previous, to: currentVersion })}>
        What changed in v{currentVersion}
      </Button>
    );
  }
  if (!currentComplete) return null;
  return (
    <Button type="button" variant="link" size="xs" className="h-auto px-0" onClick={() => onCompare({ from: version.version, to: currentVersion })}>
      Compare with current
    </Button>
  );
}

// VersionRow is one version: its number, size, date, summary, whether it holds
// all of its content, its author, and the compare control under them.
function VersionRow({ version: v, currentVersion, children }: { version: AssetVersion; currentVersion: number; children: ReactNode }) {
  return (
    <div
      className={`rounded-md border px-2.5 py-1.5 text-xs ${
        v.version === currentVersion ? "border-primary/40 bg-primary/5" : "border-border"
      }`}
    >
      <div className="flex items-center justify-between">
        <span className="font-medium">
          v{v.version}
          {v.version === currentVersion && <span className="ml-1.5 text-xs text-primary font-normal">(current)</span>}
        </span>
        <span className="text-muted-foreground">{formatBytes(v.size_bytes)}</span>
      </div>
      <div className="text-muted-foreground mt-0.5">{new Date(v.created_at).toLocaleString()}</div>
      {v.change_summary && <div className="text-muted-foreground mt-0.5 italic">{v.change_summary}</div>}
      {truncationOf(v.metadata) && (
        <div className="mt-0.5 text-amber-800 dark:text-amber-300" data-testid={`version-incomplete-${v.version}`}>
          {truncationLabel(truncationOf(v.metadata))}
        </div>
      )}
      {v.created_by && <div className="text-muted-foreground mt-0.5">by {v.created_by}</div>}
      {children}
    </div>
  );
}

// compareState is what the compare controls read: whether the asset's versions
// are text, the newest complete version before the current one (what "What
// changed" compares it with), and the pair "Compare two versions" opens on.
function compareState(versions: AssetVersion[], currentVersion: number, compare?: CompareTarget) {
  const complete = versions.filter(completeVersion).map((v) => v.version).sort((a, b) => a - b);
  const previous = complete.filter((n) => n < currentVersion).pop() ?? 0;
  return {
    canCompare: Boolean(compare && comparable(compare.contentType)),
    previous,
    currentComplete: complete.includes(currentVersion),
    twoVersions: complete.length >= 2 ? { from: complete[complete.length - 2] ?? previous, to: currentVersion } : null,
  };
}

// CompareFooter is "Compare two versions" under the list, and the comparison
// dialog while one is open.
function CompareFooter({
  compare,
  versions,
  currentVersion,
  twoVersions,
  pair,
  onPair,
}: {
  compare: CompareTarget;
  versions: AssetVersion[];
  currentVersion: number;
  twoVersions: ComparePair | null;
  pair: ComparePair | null;
  onPair: (p: ComparePair | null) => void;
}) {
  return (
    <>
      {twoVersions && (
        <Button type="button" variant="outline" size="sm" className="w-full" onClick={() => onPair(twoVersions)}>
          Compare two versions
        </Button>
      )}
      {pair && (
        <VersionCompareDialog
          assetId={compare.assetId}
          assetName={compare.assetName}
          contentType={compare.contentType}
          versions={versions}
          currentVersion={currentVersion}
          pair={pair}
          onClose={() => onPair(null)}
        />
      )}
    </>
  );
}
