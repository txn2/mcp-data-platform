export const PER_PAGE = 20;

export const INSIGHT_CATEGORIES = [
  "correction",
  "business_context",
  "data_quality",
  "usage_guidance",
  "relationship",
  "enhancement",
];

export const INSIGHT_CONFIDENCES = ["high", "medium", "low"];

export const INSIGHT_STATUSES = [
  "pending",
  "approved",
  "rejected",
  "applied",
  "superseded",
  "rolled_back",
];

export type BadgeVariant = "success" | "error" | "warning" | "neutral";

export function insightStatusVariant(status: string): BadgeVariant {
  switch (status) {
    case "pending":
      return "warning";
    case "approved":
    case "applied":
      return "success";
    case "rejected":
    case "rolled_back":
      return "error";
    case "superseded":
      return "neutral";
    default:
      return "neutral";
  }
}

export function formatCategory(cat: string): string {
  return cat.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

const MS_PER_DAY = 86_400_000;
const STALE_DAYS = 30;
const AGING_DAYS = 7;

// ageInDays returns the whole-day age of an ISO timestamp, floored at 0 so a
// clock skew that puts created_at slightly in the future never goes negative.
export function ageInDays(iso: string): number {
  return Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / MS_PER_DAY));
}

// ageBucketVariant buckets an age (days) into a badge color: red past the stale
// threshold, amber while aging, neutral when fresh (#764).
export function ageBucketVariant(days: number): BadgeVariant {
  if (days >= STALE_DAYS) return "error";
  if (days >= AGING_DAYS) return "warning";
  return "neutral";
}

export function formatAge(days: number): string {
  if (days <= 0) return "today";
  if (days === 1) return "1 day";
  return `${days} days`;
}
