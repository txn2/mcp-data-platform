import { useAssetLinks, useAssetMarkHistory } from "@/api/portal/hooks/assetMarks";
import type { Asset, AssetMarkEvent } from "@/api/portal/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { STATUS_LABEL, shortDate } from "./AssetMarkBadges";

/**
 * The details panel's part of an asset's status, Verified badge and links: who
 * verified it, the assets linking to it (#2432) and the history of its status
 * and verification (#2502, #2503). Each section shows only when it has
 * something to say.
 */
export function AssetMarksSidebar({
  asset,
  assetPath,
  onNavigate,
}: {
  asset: Pick<Asset, "id" | "verification">;
  assetPath?: (assetId: string) => string;
  onNavigate?: (path: string) => void;
}) {
  return (
    <>
      <VerifiedSection asset={asset} />
      <LinkedFromSection assetId={asset.id} assetPath={assetPath} onNavigate={onNavigate} />
      <MarkHistorySection assetId={asset.id} />
    </>
  );
}

/** Who verified the asset, the version, the note and the expiry. */
function VerifiedSection({ asset }: { asset: Pick<Asset, "verification"> }) {
  const v = asset.verification;
  if (!v) return null;
  return (
    <div className="border-t pt-4" data-testid="asset-verified-detail">
      <div className="flex flex-col gap-0.5 rounded-md border border-blue-200 bg-blue-50 px-3 py-2.5 text-sm dark:border-blue-900 dark:bg-blue-950/60">
        <span>
          <b className="font-semibold">Verified</b> by {v.verified_by}
          {shortDate(v.verified_at) ? ` · ${shortDate(v.verified_at)}` : ""} · version {v.verified_version}
        </span>
        {v.note && <span className="text-muted-foreground">"{v.note}"</span>}
        {v.expires_at && <span className="text-xs text-muted-foreground">Expires {shortDate(v.expires_at)}</span>}
      </div>
    </div>
  );
}

/**
 * The assets whose current content links to this one and that this reader can
 * open (#2432). One the reader cannot open is neither listed nor counted, and
 * with none the section is not shown.
 */
export function LinkedFromSection({
  assetId,
  assetPath,
  onNavigate,
}: {
  assetId: string;
  assetPath?: (assetId: string) => string;
  onNavigate?: (path: string) => void;
}) {
  const { data } = useAssetLinks(assetId);
  const from = data?.linked_from ?? [];
  if (from.length === 0) return null;
  return (
    <div className="border-t pt-4">
      <SectionCard data-testid="asset-linked-from" title="Linked from">
        <ul className="space-y-1">
          {from.map((a) => (
            <li key={a.id} className="text-sm">
              {assetPath && onNavigate ? (
                <button
                  type="button"
                  onClick={() => onNavigate(assetPath(a.id))}
                  className="min-w-0 truncate text-left text-primary hover:underline"
                >
                  {a.name}
                </button>
              ) : (
                <span className="truncate">{a.name}</span>
              )}
            </li>
          ))}
        </ul>
      </SectionCard>
    </div>
  );
}

/** One history line: what changed, by whom, when. */
export function describeMarkEvent(e: AssetMarkEvent): string {
  const label = (v?: string) => (v && v in STATUS_LABEL ? STATUS_LABEL[v as keyof typeof STATUS_LABEL] : v);
  if (e.kind === "verification") {
    return e.to ? "Verified" : "Verification removed";
  }
  if (!e.to) return `Status cleared (was ${label(e.from)})`;
  return e.from ? `Status ${label(e.from)} → ${label(e.to)}` : `Status set to ${label(e.to)}`;
}

/** The asset's status and verification changes, newest first. */
function MarkHistorySection({ assetId }: { assetId: string }) {
  const { data } = useAssetMarkHistory(assetId);
  const events = data ?? [];
  if (events.length === 0) return null;
  return (
    <div className="border-t pt-4">
      <SectionCard data-testid="asset-status-history" title="Status history" collapsible defaultOpen={false} summary={`${events.length} change${events.length === 1 ? "" : "s"}`}>
        <ul className="space-y-1.5">
          {events.map((e, i) => (
            <li key={`${e.at}-${i}`} className="text-xs">
              <span className="font-medium">{describeMarkEvent(e)}</span>
              {e.note && <span className="text-muted-foreground"> · {e.note}</span>}
              <span className="block text-muted-foreground">
                {e.actor}
                {shortDate(e.at) ? `, ${shortDate(e.at)}` : ""}
              </span>
            </li>
          ))}
        </ul>
      </SectionCard>
    </div>
  );
}
