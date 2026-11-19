import type { Asset, AssetStatus, AssetVerification } from "@/api/portal/types";
import { shortDate } from "./AssetMarkBadges";

/**
 * What every reader sees above a Deprecated asset's content: the status, its
 * note, who marked it and when (#2502). And, on a verified asset with a
 * version newer than the one checked, which version was verified (#2503).
 * Nothing on any other asset.
 */
export function AssetMarkBanner({
  asset,
  compact,
  onViewVersion,
}: {
  asset: Pick<Asset, "status" | "verification" | "current_version">;
  /** On a phone the title's Changed since verified badge says it, and the
   * content keeps its place under the one toolbar row (#2601); the Deprecated
   * banner is every reader's and stays. */
  compact?: boolean;
  /** Opens the verified version, where the page can show an earlier one. */
  onViewVersion?: (version: number) => void;
}) {
  const deprecated = asset.status?.value === "deprecated" ? asset.status : undefined;
  const changed = !compact && asset.verification?.changed_since_verified ? asset.verification : undefined;
  if (!deprecated && !changed) return null;
  return (
    <div className="space-y-2">
      {deprecated && <DeprecatedBanner status={deprecated} />}
      {changed && <ChangedNote verification={changed} current={asset.current_version} onViewVersion={onViewVersion} />}
    </div>
  );
}

function DeprecatedBanner({ status }: { status: AssetStatus }) {
  const on = shortDate(status.set_at);
  return (
    <div
      role="note"
      data-testid="asset-deprecated-banner"
      className="flex flex-col gap-0.5 rounded-md border border-orange-200 bg-orange-50 px-3 py-2.5 text-sm dark:border-orange-900 dark:bg-orange-950/60"
    >
      <b className="font-semibold text-orange-800 dark:text-orange-300">Deprecated</b>
      {status.note && <span className="text-orange-950 dark:text-orange-100">{status.note}</span>}
      <span className="text-xs text-orange-900 dark:text-orange-200">
        Marked by {status.set_by}
        {on ? `, ${on}` : ""}
      </span>
    </div>
  );
}

function ChangedNote({
  verification,
  current,
  onViewVersion,
}: {
  verification: AssetVerification;
  current: number;
  onViewVersion?: (version: number) => void;
}) {
  const version = verification.verified_version;
  return (
    <p data-testid="asset-changed-since-verified-note" className="text-sm text-muted-foreground">
      Verified version {version} by {verification.verified_by}. You are viewing version {current}.{" "}
      {onViewVersion && (
        <button type="button" className="text-primary hover:underline" onClick={() => onViewVersion(version)}>
          View version {version}
        </button>
      )}
    </p>
  );
}
