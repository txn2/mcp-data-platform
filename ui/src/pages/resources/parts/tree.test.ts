import { describe, it, expect } from "vitest";
import type { Folder } from "@/api/resources/types";
import { folderPaths, isUnder, joinPath, parentPath, segments } from "./tree";

// The folder list the dialogs complete from, and the path arithmetic the file
// manager moves and renames with.

function folder(path: string, count = 1): Folder {
  return { path, count };
}

describe("every folder in the library", () => {
  // A picker offers what the server reports, which already includes the levels
  // above a leaf: those are exactly where somebody moving a file wants to put
  // it, and they hold resources of their own.
  it("lists every folder path, in order", () => {
    expect(folderPaths([folder("other"), folder("data/media-manager"), folder("data")])).toEqual([
      "data",
      "data/media-manager",
      "other",
    ]);
  });

  it("is empty for a library with nothing in it", () => {
    expect(folderPaths([])).toEqual([]);
  });
});

describe("path arithmetic", () => {
  it("splits a path into folders and reads the root as none", () => {
    expect(segments("a/b")).toEqual(["a", "b"]);
    expect(segments("")).toEqual([]);
  });

  it("names the folder above, with the root above itself", () => {
    expect(parentPath("a/b/c")).toBe("a/b");
    expect(parentPath("a")).toBe("");
    expect(parentPath("")).toBe("");
  });

  it("joins onto the root without a leading slash", () => {
    expect(joinPath("", "data")).toBe("data");
    expect(joinPath("data", "shows")).toBe("data/shows");
  });

  it("counts the separator when asking whether one path is under another", () => {
    expect(isUnder("data/shows", "data")).toBe(true);
    expect(isUnder("data-archive", "data")).toBe(false);
    expect(isUnder("anything", "")).toBe(true);
  });
});
