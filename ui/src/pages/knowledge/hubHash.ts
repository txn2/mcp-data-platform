// The Knowledge hub's URL-hash contract. The top tabs and the Insights
// sub-tab are addressable so a view can be linked to from outside the app --
// the review-queue staleness alert email links straight to the queue (#803) --
// and so a refresh lands where the reader was.
//
// It lives apart from the component because it is the part other systems
// depend on: a link that stops resolving is a broken promise made in an email
// that was already sent.

export type Tab = "knowledge" | "insights" | "memory";

// The Insights tab splits your own captured insights from the reviewer queue.
export type InsightSubTab = "mine" | "review";

// REVIEW_HASH addresses the review queue directly (/knowledge#review), rather
// than the Insights tab it lives behind.
export const REVIEW_HASH = "review";

// normalizeTab picks the top tab a hash addresses, defaulting to Knowledge for
// anything unrecognized.
export function normalizeTab(raw?: string): Tab {
  if (raw === "insights" || raw === "memory") return raw;
  return raw === REVIEW_HASH ? "insights" : "knowledge";
}

// normalizeInsightSub picks the Insights sub-tab a hash addresses. Only the
// review queue is addressable; anything else opens a reviewer's own insights.
export function normalizeInsightSub(raw?: string): InsightSubTab {
  return raw === REVIEW_HASH ? "review" : "mine";
}

// insightSubHash is the hash that re-opens the given Insights sub-tab.
export function insightSubHash(sub: InsightSubTab): string {
  return sub === "review" ? REVIEW_HASH : "insights";
}
