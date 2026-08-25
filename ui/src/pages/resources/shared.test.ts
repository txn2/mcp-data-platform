import { describe, it, expect } from "vitest";
import { CATEGORIES, CATEGORY_HINTS } from "./shared";

// The built-in categories are read by three surfaces at once -- the upload
// dialog, the edit dialog, and the library's category filter -- so the list is
// asserted here rather than three times over rendered markup.

describe("the built-in resource categories", () => {
  it("offers data alongside the other four", () => {
    expect([...CATEGORIES]).toEqual(["data", "samples", "playbooks", "templates", "references"]);
  });

  // A category with no hint is offered with nothing said about what it means,
  // which is the whole reason the built-in list exists rather than a free-text
  // box: the dialog spells each one out as it is chosen.
  it("says what every one of them is for", () => {
    for (const c of CATEGORIES) {
      expect(CATEGORY_HINTS[c], `no hint for "${c}"`).toBeTruthy();
    }
  });

  // data and samples are the pair most easily confused -- the same CSV is a
  // sample when the agent should copy its shape and data when it should read
  // its rows -- so each hint has to draw that line rather than restate "a file".
  it("separates data from samples in the words the reader sees", () => {
    expect(CATEGORY_HINTS["data"]).toMatch(/facts|fact/i);
    expect(CATEGORY_HINTS["samples"]).toMatch(/pattern-match|example/i);
  });
});
