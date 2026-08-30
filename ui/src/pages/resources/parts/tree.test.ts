import { describe, it, expect } from "vitest";
import type { Folder } from "@/api/resources/types";
import { childFolders, folderPaths, isUnder, joinPath, parentPath, segments } from "./tree";

// A folder exists because a resource is filed under it, and the server says
// which folders those are and how much each holds (#1555). What is asserted
// here is that the level drawn from that answer is the level a person expects,
// including the two cases a naive prefix comparison gets wrong: a sibling whose
// name starts with the same letters, and a folder deeper than the level in view.

function folder(path: string, count = 1): Folder {
  return { path, count };
}

describe("the level drawn from the server's folders", () => {
  // What the server answers for a library filed like this: every folder that
  // holds anything, each counting everything beneath it at every depth.
  const library = [
    folder("data", 4),
    folder("data/media-manager", 2),
    folder("data/media-manager/shows", 1),
    folder("data/weekly", 1),
    folder("data-archive", 1),
    folder("other", 1),
  ];

  it("shows the folders at the root", () => {
    expect(childFolders(library, "").map((f) => f.name)).toEqual([
      "data",
      "data-archive",
      "other",
    ]);
  });

  it("carries the server's count, which is everything beneath at every depth", () => {
    expect(childFolders(library, "").find((f) => f.name === "data")?.count).toBe(4);
  });

  it("shows one level down when a folder is opened", () => {
    expect(childFolders(library, "data").map((f) => f.name)).toEqual([
      "media-manager",
      "weekly",
    ]);
  });

  it("keeps going down", () => {
    expect(childFolders(library, "data/media-manager").map((f) => f.name)).toEqual(["shows"]);
    expect(childFolders(library, "data/media-manager/shows")).toEqual([]);
  });

  // The one a prefix comparison without the separator gets wrong.
  it("does not draw a sibling into a folder whose name it starts with", () => {
    expect(childFolders(library, "data").map((f) => f.name)).not.toContain("archive");
  });

  // A grandchild is inside one of the children and is already counted in it;
  // drawing it here would show the same file twice at one level.
  it("shows only the level directly below, not everything beneath", () => {
    expect(childFolders(library, "").map((f) => f.name)).not.toContain("media-manager");
  });

  it("orders folders by name", () => {
    expect(childFolders([folder("data/b"), folder("data/a")], "data").map((f) => f.name)).toEqual([
      "a",
      "b",
    ]);
  });

  it("shows nothing for a folder nothing is filed under", () => {
    expect(childFolders(library, "nowhere")).toEqual([]);
  });

  it("carries the full path each folder opens at", () => {
    expect(childFolders(library, "data").map((f) => f.path)).toEqual([
      "data/media-manager",
      "data/weekly",
    ]);
  });
});

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
