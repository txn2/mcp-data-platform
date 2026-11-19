import { Check } from "lucide-react";
import type { Asset, AssetStatusValue } from "@/api/portal/types";
import { cn } from "@/lib/utils";

/**
 * An asset's status and Verified badges (#2502, #2503), as the viewer header,
 * a card and a table row show them. A status grants nothing; the badge only
 * says how far along the asset is.
 */

/** The label each status reads as. */
export const STATUS_LABEL: Record<AssetStatusValue, string> = {
  draft: "Draft",
  published: "Published",
  deprecated: "Deprecated",
};

/** The one-line meaning each status carries in the Status menu. */
export const STATUS_MEANING: Record<AssetStatusValue, string> = {
  draft: "still being worked on",
  published: "finished, ready to use",
  deprecated: "with a note",
};

const STATUS_TONE: Record<AssetStatusValue, string> = {
  draft: "border border-slate-300 bg-slate-100 text-slate-700 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-200",
  published: "bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-300",
  deprecated: "bg-orange-100 text-orange-800 dark:bg-orange-950 dark:text-orange-300",
};

const PILL = "inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-medium leading-none whitespace-nowrap";

/** A status as a pill. */
export function StatusBadge({ status, small }: { status: AssetStatusValue; small?: boolean }) {
  return (
    <span data-testid="asset-status-badge" className={cn(PILL, small ? "text-[11px]" : "text-xs", STATUS_TONE[status])}>
      {STATUS_LABEL[status]}
    </span>
  );
}

/** The Verified badge. */
export function VerifiedBadge({ small }: { small?: boolean }) {
  return (
    <span
      data-testid="asset-verified-badge"
      className={cn(PILL, small ? "text-[11px]" : "text-xs", "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-300")}
    >
      <Check className={small ? "size-2.5" : "size-3"} strokeWidth={3} aria-hidden />
      Verified
    </span>
  );
}

/** The marker on a verified asset that has a newer version than the one checked. */
export function ChangedSinceVerifiedBadge() {
  return (
    <span
      data-testid="asset-changed-since-verified"
      className={cn(PILL, "text-xs bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300")}
    >
      Changed since verified
    </span>
  );
}

/**
 * Every badge an asset carries, in reading order: its status, Verified, and
 * Changed since verified. Nothing when it carries none. `small` is the card
 * and table size, which leaves the Changed since verified marker to the viewer.
 */
export function AssetMarkBadges({
  asset,
  small,
  className,
}: {
  asset: Pick<Asset, "status" | "verification">;
  small?: boolean;
  className?: string;
}) {
  const { status, verification } = asset;
  if (!status && !verification) return null;
  return (
    <span className={cn("inline-flex flex-wrap items-center gap-1.5 align-middle", className)}>
      {status && <StatusBadge status={status.value} small={small} />}
      {verification && <VerifiedBadge small={small} />}
      {verification?.changed_since_verified && !small && <ChangedSinceVerifiedBadge />}
    </span>
  );
}

/** A short date ("Nov 12"), with the year when it is not this one. */
export function shortDate(iso: string | undefined): string {
  if (!iso) return "";
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  const thisYear = at.getFullYear() === new Date().getFullYear();
  return at.toLocaleDateString(undefined, { month: "short", day: "numeric", year: thisYear ? undefined : "numeric" });
}
