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

/** How a version names itself, in the trigger and in the list alike. */
function versionLabel(version: number, currentVersion: number): string {
  return `v${version}${version === currentVersion ? " (current)" : ""}`;
}

/**
 * When a version was written, for the picker's option list.
 *
 * A scheduled script refreshes an asset hourly, so the version number alone
 * does not say which entry to open (#1422). Compact by design: the seconds go,
 * and the rest is left to the reader's locale, since this sits beside the
 * version number in a dropdown. The year is carried only for a version not
 * from the current one — a history long enough to span a new year is exactly
 * where a bare month and day stops identifying anything. A timestamp that will
 * not parse renders as nothing rather than "Invalid Date".
 */
function versionTime(iso: string): string {
  if (!iso) return "";
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  const thisYear = at.getFullYear() === new Date().getFullYear();
  return at.toLocaleString(undefined, {
    year: thisYear ? undefined : "numeric",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
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

  const shown = selectedVersion ?? asset.current_version;

  return (
    <>
      <Select
        value={String(shown)}
        onValueChange={(v) => {
          const version = Number(v);
          // The current version is the viewer's default state, not a selection,
          // so choosing it clears the selection rather than pinning it.
          onSelectVersion(version === asset.current_version ? null : version);
        }}
      >
        <SelectTrigger size="sm" aria-label="Asset version" className="w-36">
          {/*
            Giving SelectValue children stops Radix portaling the selected
            item's text into the trigger, which is what keeps the timestamp in
            the list and out of a trigger only 36 units wide.
          */}
          <SelectValue>{versionLabel(shown, asset.current_version)}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {versions.map((v) => {
            const at = versionTime(v.created_at);
            return (
              <SelectItem
                key={v.version}
                value={String(v.version)}
                className="[&>span:last-child]:w-full [&>span:last-child]:justify-between"
              >
                <span className="whitespace-nowrap">
                  {versionLabel(v.version, asset.current_version)}
                </span>
                {at && (
                  <span className="whitespace-nowrap text-xs text-muted-foreground">
                    {at}
                  </span>
                )}
              </SelectItem>
            );
          })}
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
