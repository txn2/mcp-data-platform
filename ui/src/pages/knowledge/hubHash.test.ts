import { describe, it, expect } from "vitest";
import {
  REVIEW_HASH,
  insightSubHash,
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
});
