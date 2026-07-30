import type { PromptUsage } from "@/api/admin/types";

// Usage presentation helpers for the prompt library (#1010). Usage data is the
// audit-derived rollup from GET /portal/prompts/usage, joined client-side onto
// the prompt lists.

// staleAfterDays is the age at which a previously used prompt reads as
// inactive in the library ("long-unrun" per the library's dead-prompt rule).
export const staleAfterDays = 60;

// newPromptGraceDays suppresses the never-run flag on prompts too young to
// have a usage history: a prompt created moments ago is not a dead one (#1124).
export const newPromptGraceDays = 7;

const dayMs = 24 * 60 * 60 * 1000;

// isInactive reports whether a prompt should be visually identifiable as dead:
// never run at all, or last run longer than staleAfterDays ago.
export function isInactive(usage: PromptUsage | undefined, now = new Date()): boolean {
  if (!usage || usage.run_count === 0) return true;
  if (!usage.last_run_at) return true;
  const ageMs = now.getTime() - new Date(usage.last_run_at).getTime();
  return ageMs > staleAfterDays * dayMs;
}

// UsageBadgeInfo is a row flag naming the exact usage condition it measures,
// so it cannot be misread as a lifecycle state like the old "inactive" label
// was (#1124).
export interface UsageBadgeInfo {
  label: string;
  title: string;
}

// usageBadge returns the flag for a dead prompt ("never run", or unused beyond
// staleAfterDays), or null when the prompt is in active use or was created
// within newPromptGraceDays and has simply not had time to be run yet.
export function usageBadge(
  usage: PromptUsage | undefined,
  createdAt: string | undefined,
  now = new Date(),
): UsageBadgeInfo | null {
  if (!usage || usage.run_count === 0 || !usage.last_run_at) {
    const created = createdAt ? new Date(createdAt).getTime() : NaN;
    if (!Number.isNaN(created) && now.getTime() - created < newPromptGraceDays * dayMs) return null;
    return { label: "never run", title: "No recorded runs within the audit retention window" };
  }
  const ageMs = now.getTime() - new Date(usage.last_run_at).getTime();
  if (ageMs > staleAfterDays * dayMs) {
    return {
      label: `unused ${staleAfterDays}d+`,
      title: `Last run more than ${staleAfterDays} days ago`,
    };
  }
  return null;
}

// formatLastRun renders a compact relative label for the last-run timestamp.
export function formatLastRun(usage: PromptUsage | undefined, now = new Date()): string {
  if (!usage || usage.run_count === 0 || !usage.last_run_at) return "Never";
  const days = Math.floor((now.getTime() - new Date(usage.last_run_at).getTime()) / (24 * 60 * 60 * 1000));
  if (days <= 0) return "Today";
  if (days === 1) return "Yesterday";
  if (days < 30) return `${days}d ago`;
  if (days < 365) return `${Math.floor(days / 30)}mo ago`;
  return `${Math.floor(days / 365)}y ago`;
}

// UsageFacet narrows the library by activity.
export type UsageFacet = "all" | "active" | "inactive";

export function matchesUsageFacet(facet: UsageFacet, usage: PromptUsage | undefined, now = new Date()): boolean {
  if (facet === "all") return true;
  return facet === "inactive" ? isInactive(usage, now) : !isInactive(usage, now);
}

// usageSortValue orders prompts for the run-count and last-run sorts; missing
// usage sorts as zero / epoch so unused prompts sink on descending sorts.
export function usageSortValue(key: "runs" | "lastRun", usage: PromptUsage | undefined): number {
  if (!usage) return 0;
  if (key === "runs") return usage.run_count;
  return usage.last_run_at ? new Date(usage.last_run_at).getTime() : 0;
}

