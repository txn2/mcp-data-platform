import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

/**
 * How much version history an asset keeps, as the three states the column
 * carries: inherit the deployment default, keep everything, or keep a number.
 *
 * The value is the wire value: `undefined` inherits (sent as null), 0 keeps
 * every version, and a positive number is the cap. It is a select rather than a
 * bare number field because "keep everything" is spelled 0 on the wire, which
 * nobody would guess from a box asking for a count.
 */
export type RetentionMode = "default" | "unlimited" | "custom";

export function retentionModeFor(maxVersions: number | null | undefined): RetentionMode {
  if (maxVersions === null || maxVersions === undefined) return "default";
  return maxVersions === 0 ? "unlimited" : "custom";
}

/** Whether a typed count is a cap the platform can act on. */
export function retentionCountValid(mode: RetentionMode, custom: string): boolean {
  if (mode !== "custom") return true;
  const n = Number.parseInt(custom, 10);
  return Number.isFinite(n) && n > 0;
}

/**
 * Whether a mode plus a count still describes what the asset already carries.
 * An unchanged setting is left out of the update entirely, so an editor who
 * renamed an asset never sends the one field the server reserves to the owner.
 */
export function retentionUnchanged(
  mode: RetentionMode,
  custom: string,
  stored: number | null | undefined,
): boolean {
  return retentionValue(mode, custom) === (stored ?? null);
}

/**
 * The wire value a mode plus a typed count resolves to.
 *
 * An unfinished count falls back to the deployment default rather than to a
 * number: 0 on the wire means "keep everything" and 1 means "keep almost
 * nothing", so guessing either way from a blank box would either ignore the
 * person's intent or delete history they never agreed to lose. Save is disabled
 * in that state, so this is the backstop rather than the path.
 */
export function retentionValue(mode: RetentionMode, custom: string): number | null {
  if (mode === "default") return null;
  if (mode === "unlimited") return 0;
  if (!retentionCountValid(mode, custom)) return null;
  return Number.parseInt(custom, 10);
}

export function AssetRetentionField({
  mode,
  custom,
  onModeChange,
  onCustomChange,
  id,
}: {
  mode: RetentionMode;
  /** The count field's raw text, so a half-typed number is not clobbered. */
  custom: string;
  onModeChange: (m: RetentionMode) => void;
  onCustomChange: (v: string) => void;
  id: string;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={`${id}-retention`} className="text-xs text-muted-foreground">
        Version history
      </Label>
      <Select value={mode} onValueChange={(v) => onModeChange(v as RetentionMode)}>
        <SelectTrigger id={`${id}-retention`} size="sm" className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="default">Deployment default</SelectItem>
          <SelectItem value="custom">Keep the newest…</SelectItem>
          <SelectItem value="unlimited">Keep every version</SelectItem>
        </SelectContent>
      </Select>
      {mode === "custom" && (
        <>
          <Input
            aria-label="Versions to keep"
            type="number"
            min={1}
            value={custom}
            onChange={(e) => onCustomChange(e.target.value)}
          />
          {!retentionCountValid(mode, custom) && (
            <p className="text-xs text-destructive">Enter how many versions to keep (1 or more).</p>
          )}
        </>
      )}
      <p className="text-xs text-muted-foreground">
        Older versions are deleted along with their stored content once a new version pushes them past
        the limit.
      </p>
    </div>
  );
}
