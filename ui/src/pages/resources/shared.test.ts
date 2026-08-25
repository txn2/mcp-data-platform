import { describe, it, expect } from "vitest";
import { CATEGORIES, CATEGORY_HINTS } from "./shared";

// The built-in categories are read by three surfaces at once -- the upload
// dialog, the edit dialog, and the library's category filter -- so the list is
// asserted here rather than three times over rendered markup.

describe("the built-in resource categories", () => {
  // The four the set began with all name a text document for an agent to read.
  // A stored dataset and a logo had no home among them, so a CSV filed under
  // `references` was described to its uploader as a background document to
  // consult (#1471).
  it("offers data and visual alongside the other four", () => {
    expect([...CATEGORIES]).toEqual([
      "data",
      "visual",
      "samples",
      "playbooks",
      "templates",
      "references",
    ]);
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

  // visual is the one category named for what the file IS rather than for what
  // the agent does with it, so its hint has to name the things a reader would
  // otherwise file under a prose category.
  it("names what belongs under visual", () => {
    expect(CATEGORY_HINTS["visual"]).toMatch(/logo/i);
    expect(CATEGORY_HINTS["visual"]).toMatch(/photograph|diagram/i);
  });
});
