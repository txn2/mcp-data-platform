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
];

// isReturnedToReview marks a pending insight that was already applied once and
// came back when its changeset was rolled back (#1257). A rollback undoes the
// application rather than discarding the insight, so the queue holds it again —
// and the application it carries is the thing that tells a reviewer this one has
// a history, not that it is a fresh capture.
export function isReturnedToReview(insight: {
  status: string;
  changeset_ref?: string;
}): boolean {
  return insight.status === "pending" && !!insight.changeset_ref;
}

export type BadgeVariant = "success" | "error" | "warning" | "neutral";

// confidenceVariant tints how sure the capture was. Low confidence is neutral
// rather than red: an unsure insight is not a failure, it is one a reviewer
// should read more closely.
export function confidenceVariant(confidence: string): BadgeVariant {
  if (confidence === "high") return "success";
  if (confidence === "medium") return "warning";
  return "neutral";
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
