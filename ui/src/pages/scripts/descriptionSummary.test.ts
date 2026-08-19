import { describe, it, expect } from "vitest";
import { descriptionSummary } from "./descriptionSummary";

// A queue row has one line for a description, and a description is a markdown
// document (#1369). These are the shapes a real one takes.
describe("descriptionSummary", () => {
  it("takes the opening prose of a document rather than its first heading", () => {
    const doc = [
      "## What it produces",
      "",
      "One CSV asset with a row per region.",
      "",
      "## What it assumes",
    ].join("\n");
    expect(descriptionSummary(doc)).toBe("One CSV asset with a row per region.");
  });

  it("returns a one-line description unchanged", () => {
    expect(descriptionSummary("Yesterday's sales by region.")).toBe(
      "Yesterday's sales by region.",
    );
  });

  it("skips fenced code, which is an example rather than an explanation", () => {
    const doc = ["```sql", "SELECT 1", "```", "", "Rows per region."].join("\n");
    expect(descriptionSummary(doc)).toBe("Rows per region.");
  });

  it("strips the inline markers a plain line would show as punctuation", () => {
    expect(descriptionSummary("Reads **sales.orders** via `platform.query`.")).toBe(
      "Reads sales.orders via platform.query.",
    );
    expect(descriptionSummary("See [the runbook](https://example.com/runbook).")).toBe(
      "See the runbook.",
    );
  });

  it("falls back to the heading when a document is nothing but structure", () => {
    expect(descriptionSummary("## Daily sales\n\n- region\n- revenue")).toBe("Daily sales");
  });

  it("bounds a long opening paragraph so a row stays a row", () => {
    const summary = descriptionSummary("x".repeat(400));
    expect(summary.length).toBeLessThanOrEqual(181);
    expect(summary.endsWith("…")).toBe(true);
  });

  it("has nothing to say about an empty description", () => {
    expect(descriptionSummary("")).toBe("");
  });
});
