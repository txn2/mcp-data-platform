import { describe, it, expect } from "vitest";
import { visibleFacetTags, type TagCount } from "./tagFacet";

// Build N tag/count pairs sorted most-used-first (count descending).
function tags(n: number): TagCount[] {
  return Array.from({ length: n }, (_, i) => [`tag${i}`, n - i] as TagCount);
}

describe("visibleFacetTags (#707)", () => {
  it("returns the full list when at or under the limit", () => {
    const t = tags(5);
    expect(visibleFacetTags(t, "", false, 20)).toEqual(t);
  });

  it("caps to the top N when collapsed and over the limit", () => {
    const t = tags(30);
    const visible = visibleFacetTags(t, "", false, 20);
    expect(visible).toHaveLength(20);
    expect(visible).toEqual(t.slice(0, 20));
  });

  it("returns the full list when expanded", () => {
    const t = tags(30);
    expect(visibleFacetTags(t, "", true, 20)).toEqual(t);
  });

  it("keeps a selected tag visible even when it falls outside the top N", () => {
    const t = tags(30);
    const hidden = "tag25"; // index 25, outside the top 20
    const visible = visibleFacetTags(t, hidden, false, 20);
    expect(visible).toHaveLength(21); // top 20 + the appended selected tag
    expect(visible.map(([name]) => name)).toContain(hidden);
  });

  it("does not duplicate a selected tag that is already in the top N", () => {
    const t = tags(30);
    const inTop = "tag3"; // index 3, within the top 20
    const visible = visibleFacetTags(t, inTop, false, 20);
    expect(visible).toHaveLength(20);
    expect(visible.filter(([name]) => name === inTop)).toHaveLength(1);
  });

  it("leaves nothing hidden when the only over-limit tag is the selected one", () => {
    // Exactly limit+1 tags with the single over-limit tag selected: it is pulled
    // into view, so every tag is visible and the caller must not show a reveal
    // control (its toggle would be a dead button) (#707).
    const t = tags(21);
    const visible = visibleFacetTags(t, "tag20", false, 20);
    expect(visible).toHaveLength(t.length); // all 21 shown
    expect(t.length - visible.length).toBe(0); // nothing hidden -> no reveal
  });

  it("ignores a selected tag that is not in the list", () => {
    const t = tags(30);
    const visible = visibleFacetTags(t, "does-not-exist", false, 20);
    expect(visible).toHaveLength(20);
    expect(visible).toEqual(t.slice(0, 20));
  });
});
