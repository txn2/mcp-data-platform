import type { InsightStats } from "@/api/admin/types";
import { StatCard } from "@/components/cards/StatCard";
import { ageInDays, formatAge, formatCategory } from "./helpers";

// Review-queue staleness (#764): how old the oldest pending insight is, and how
// much debt has crossed the 30-day threshold. The tile is tinted by the worse of
// the two so an aging queue is visible before it is read.
function pendingSummary(stats: InsightStats | undefined) {
  const oldestDays = stats?.oldest_pending_at ? ageInDays(stats.oldest_pending_at) : null;
  const over30d = stats?.pending_over_30d ?? 0;
  const detail =
    oldestDays === null
      ? undefined
      : `Oldest ${formatAge(oldestDays)}` + (over30d > 0 ? ` · ${over30d} over 30d` : "");
  let tint: string | undefined;
  if (over30d > 0) tint = "border-destructive/50";
  else if (stats && stats.total_pending > 0) tint = "border-amber-400/60";
  return { detail, tint };
}

// topCategory is what the queue is mostly about, or "-" before any insight has
// been captured.
function topCategory(stats: InsightStats | undefined): string {
  const entries = Object.entries(stats?.by_category ?? {});
  if (entries.length === 0) return "-";
  entries.sort((a, b) => b[1] - a[1]);
  return formatCategory(entries[0]![0]);
}

// totalInsights counts every insight ever captured: each one is in exactly one
// status, so the statuses sum to the whole.
function totalInsights(stats: InsightStats | undefined): string {
  if (!stats?.by_status) return "-";
  return Object.values(stats.by_status)
    .reduce((s, n) => s + n, 0)
    .toLocaleString();
}

// InsightStatsRow is the review queue's summary: what is waiting, how much has
// been captured, what it is about, and how much of it landed.
export function InsightStatsRow({ stats }: { stats: InsightStats | undefined }) {
  const pending = pendingSummary(stats);
  return (
    <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
      <StatCard
        label="Pending Review"
        value={stats?.total_pending ?? "-"}
        detail={pending.detail}
        className={pending.tint}
      />
      <StatCard label="Total Insights" value={totalInsights(stats)} />
      <StatCard label="Top Category" value={topCategory(stats)} />
      <StatCard label="Applied" value={stats?.by_status?.["applied"] ?? "-"} />
    </div>
  );
}
