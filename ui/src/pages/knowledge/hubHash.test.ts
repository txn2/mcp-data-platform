import { describe, it, expect } from "vitest";
import {
  REVIEW_HASH,
  catalogSubHash,
  insightSubHash,
  normalizeCatalogSub,
  normalizeInsightSub,
  normalizeTab,
} from "./hubHash";

describe("knowledge hub hash routing", () => {
  it("opens the review queue from the deep link the alert email sends (#803)", () => {
    // The link in the email is <base>/portal/knowledge#review. Both halves of
    // the view it addresses have to come from that one hash.
    expect(normalizeTab(REVIEW_HASH)).toBe("insights");
    expect(normalizeInsightSub(REVIEW_HASH)).toBe("review");
  });

  it("keeps the existing top-tab hashes working", () => {
    expect(normalizeTab("insights")).toBe("insights");
    expect(normalizeTab("memory")).toBe("memory");
    expect(normalizeTab("changesets")).toBe("knowledge");
    expect(normalizeTab(undefined)).toBe("knowledge");
  });

  it("opens a reviewer's own insights for every hash but the review one", () => {
    expect(normalizeInsightSub("insights")).toBe("mine");
    expect(normalizeInsightSub(undefined)).toBe("mine");
  });

  it("round-trips a selected sub-tab through its hash", () => {
    expect(normalizeInsightSub(insightSubHash("review"))).toBe("review");
    expect(normalizeInsightSub(insightSubHash("mine"))).toBe("mine");
    expect(normalizeTab(insightSubHash("mine"))).toBe("insights");
  });

  it("round-trips every Catalog inner tab through its hash (#1194)", () => {
    for (const sub of ["tables", "context_docs", "tags", "domains", "glossary"] as const) {
      expect(normalizeCatalogSub(catalogSubHash(sub))).toBe(sub);
    }
    // The URL spells it with a hyphen; the type member cannot.
    expect(catalogSubHash("context_docs")).toBe("context-docs");
  });

  it("opens Tables for a bare /knowledge/catalog and any unknown hash", () => {
    expect(normalizeCatalogSub(undefined)).toBe("tables");
    expect(normalizeCatalogSub("")).toBe("tables");
    // The retired routes' names must not resolve to something surprising.
    expect(normalizeCatalogSub("context_docs")).toBe("tables");
    expect(normalizeCatalogSub("lineage")).toBe("tables");
  });
});
