import { Code, Download, Eye, FileWarning, RotateCcw, Save } from "lucide-react";
import type { Asset, AssetVersion } from "@/api/portal/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatBytes } from "@/lib/format";
import type { ViewMode } from "./types";

const VIEW_OPTIONS = [
  { value: "preview" as const, label: "Preview", icon: Eye, text: "Preview" },
  { value: "source" as const, label: "Source", icon: Code, text: "Source" },
];

/** Preview/Source switch, shown only for families the editor supports. */
export function ViewModeToggle({
  show,
  viewMode,
  onSetViewMode,
}: {
  show: boolean;
  viewMode: ViewMode;
  onSetViewMode: (mode: ViewMode) => void;
}) {
  if (!show) return null;
  return (
    <SegmentedControl
      label="Content view"
      value={viewMode}
      onChange={onSetViewMode}
      options={VIEW_OPTIONS}
    />
  );
}

/** Version picker plus the revert action for an older version. */
export function VersionControls({
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
      <Select
        value={String(selectedVersion ?? asset.current_version)}
        onValueChange={(v) => {
          const version = Number(v);
          // The current version is the viewer's default state, not a selection,
          // so choosing it clears the selection rather than pinning it.
          onSelectVersion(version === asset.current_version ? null : version);
        }}
      >
        <SelectTrigger size="sm" aria-label="Asset version" className="w-36">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {versions.map((v) => (
            <SelectItem key={v.version} value={String(v.version)}>
              v{v.version}
              {v.version === asset.current_version ? " (current)" : ""}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {viewingOldVersion && canRevert && (
        <Button variant="outline" size="sm" onClick={onRevert}>
          <RotateCcw />
          Revert
        </Button>
      )}
    </>
  );
}

/** Save button and its result indicator, shown while editing source. */
export function SaveControls({
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
      <Button size="sm" onClick={onSaveContent} disabled={!hasChanges || pending}>
        <Save />
        {pending ? "Saving..." : "Save"}
      </Button>
      {saveStatus === "saved" && <Badge variant="success">Saved</Badge>}
      {saveStatus === "error" && <Badge variant="danger">Save failed</Badge>}
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
export function TooLarge({
  asset,
  sizeBytes,
  contentUrl,
}: {
  asset: Asset;
  sizeBytes: number;
  contentUrl: string;
}) {
  return (
    <EmptyState
      icon={FileWarning}
      className="py-20"
      action={
        <Button asChild>
          <a href={contentUrl} download={asset.name}>
            <Download />
            Download
          </a>
        </Button>
      }
    >
      <p className="text-lg font-medium text-foreground">Too large to preview</p>
      <p className="mt-1">
        This file is {formatBytes(sizeBytes)}, past the inline preview limit for{" "}
        {asset.content_type}.
      </p>
    </EmptyState>
  );
}
