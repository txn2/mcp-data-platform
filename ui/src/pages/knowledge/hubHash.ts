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

// The Catalog section's inner tabs (#1194). Catalog is one route holding every
// DataHub-backed surface, so the surface within it is carried in the hash the
// same way the top tabs are: /knowledge/catalog#tags re-opens the tag
// vocabulary, and /knowledge/catalog?urn=...#glossary opens that term on the
// Glossary tab.
//
// The tab set and its URL spellings live in lib/entityRefs with the rest of the
// reference route table, because the builder that turns a stored catalog
// reference into a link needs them too (#1159); a second copy here would let a
// link and the tab it addresses drift apart.
export type { CatalogSubTab } from "@/lib/entityRefs";

import { CATALOG_SUB_HASHES, type CatalogSubTab } from "@/lib/entityRefs";

// normalizeCatalogSub picks the Catalog inner tab a hash addresses, defaulting
// to Tables for anything unrecognized (including no hash at all, which is what
// a bare /knowledge/catalog arrives with).
export function normalizeCatalogSub(raw?: string): CatalogSubTab {
  for (const [sub, hash] of Object.entries(CATALOG_SUB_HASHES)) {
    if (raw === hash) return sub as CatalogSubTab;
  }
  return "tables";
}

// catalogSubHash is the hash that re-opens the given Catalog inner tab.
export function catalogSubHash(sub: CatalogSubTab): string {
  return CATALOG_SUB_HASHES[sub];
}
