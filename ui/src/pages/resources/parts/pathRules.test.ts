import { describe, it, expect } from "vitest";
import { pathProblem } from "./pathRules";

// The browser's copy of the folder-path rules. The server is the authority and
// checks every path again; what matters here is that the two agree on which
// paths are legal, and that a refusal names the rule rather than restating the
// whole grammar.

describe("paths the library accepts", () => {
  it.each([
    "data",
    "samples",
    "a",
    "data/media-manager",
    "data/media-manager/shows",
    "a/b/c/d/e/f/g/h",
    "a".repeat(31),
  ])("accepts %s", (path) => {
    expect(pathProblem(path)).toBeNull();
  });
});

describe("paths the library refuses", () => {
  it.each([
    ["", "required"],
    ["/data", "start or end"],
    ["data/", "start or end"],
    ["data//shows", "empty folder name"],
    ["data/../etc", "names no folder"],
    ["data/./shows", "names no folder"],
    ["a/b/c/d/e/f/g/h/i", "8 folders deep"],
    ["data/Shows", '"Shows" must be lowercase'],
    ["data/2024", '"2024" must be lowercase'],
    ["data/media_manager", '"media_manager" must be lowercase'],
    ["a".repeat(32), "must be lowercase"],
  ])("refuses %s and names the rule", (path, says) => {
    expect(pathProblem(path)).toContain(says);
  });

  it("names the depth rule for a path that breaks both bounds", () => {
    // Removing folders is what fixes it, so the character limit would send the
    // person shortening names that were never the problem.
    const tooDeepAndTooLong = Array(9).fill("a".repeat(31)).join("/");
    expect(pathProblem(tooDeepAndTooLong)).toContain("folders deep");
  });

  it("names the length rule for a legal-depth path that is still too long", () => {
    expect(pathProblem(Array(8).fill("a".repeat(31)).join("/"))).toContain("200 characters");
  });
});
