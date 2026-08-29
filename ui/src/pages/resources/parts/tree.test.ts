import { describe, it, expect } from "vitest";
import type { Resource } from "@/api/resources/types";
import { everyFolder, folderView, isUnder, joinPath, parentPath, segments } from "./tree";

// A folder exists because a resource is filed under it. What is asserted here
// is that the tree derived from a set of paths is the tree a person expects,
// including the two cases a naive prefix comparison gets wrong: a sibling whose
// name starts with the same letters, and a folder deeper than the level in view.

function at(path: string, id = path): Resource {
  return {
    id,
    scope: "user",
    scope_id: "sub-1",
    path,
    filename: `${id}.csv`,
    display_name: id,
    description: "",
    mime_type: "text/csv",
    size_bytes: 1,
    s3_key: "k",
    uri: `mcp://user/sub-1/${path}/${id}.csv`,
    tags: [],
    uploader_sub: "sub-1",
    uploader_email: "me@example.com",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

describe("the tree a set of paths makes", () => {
  const library = [
    at("data", "top"),
    at("data/media-manager", "mid"),
    at("data/media-manager/shows", "deep"),
    at("data/weekly", "weekly"),
    at("data-archive", "sibling"),
    at("other", "elsewhere"),
  ];

  it("shows the folders and files at the root", () => {
    const view = folderView(library, "");
    expect(view.folders.map((f) => f.name)).toEqual(["data", "data-archive", "other"]);
    // Nothing is filed at the library's own root in this fixture, so there are
    // no files beside the folders.
    expect(view.files).toEqual([]);
  });

  it("counts everything beneath a folder, at every depth", () => {
    const data = folderView(library, "").folders.find((f) => f.name === "data");
    expect(data?.count).toBe(4);
  });

  it("shows one level down when a folder is opened", () => {
    const view = folderView(library, "data");
    expect(view.folders.map((f) => f.name)).toEqual(["media-manager", "weekly"]);
    expect(view.files.map((r) => r.id)).toEqual(["top"]);
  });

  it("keeps going down", () => {
    expect(folderView(library, "data/media-manager").folders.map((f) => f.name)).toEqual(["shows"]);
    expect(folderView(library, "data/media-manager/shows").files.map((r) => r.id)).toEqual(["deep"]);
  });

  // The one a prefix comparison without the separator gets wrong.
  it("does not draw a sibling into a folder whose name it starts with", () => {
    expect(folderView(library, "data").folders.map((f) => f.name)).not.toContain("archive");
    expect(folderView(library, "data").files.map((r) => r.id)).not.toContain("sibling");
  });

  it("orders folders by name and leaves files in the order they arrived", () => {
    const view = folderView([at("data/b", "b"), at("data/a", "a"), at("data", "z"), at("data", "y")], "data");
    expect(view.folders.map((f) => f.name)).toEqual(["a", "b"]);
    expect(view.files.map((r) => r.id)).toEqual(["z", "y"]);
  });

  it("shows nothing for a folder nothing is filed under", () => {
    expect(folderView(library, "nowhere")).toEqual({ folders: [], files: [] });
  });

  it("carries the full path each folder opens at", () => {
    expect(folderView(library, "data").folders.map((f) => f.path)).toEqual([
      "data/media-manager",
      "data/weekly",
    ]);
  });
});

describe("every folder in view", () => {
  // A picker that offered only the paths resources are filed at would make the
  // levels above them unreachable, which is exactly where somebody moving a
  // file wants to put it.
  it("includes the intermediate levels, not only the leaves", () => {
    expect(everyFolder([at("data/media-manager/shows"), at("other")])).toEqual([
      "data",
      "data/media-manager",
      "data/media-manager/shows",
      "other",
    ]);
  });

  it("lists each folder once however many files are in it", () => {
    expect(everyFolder([at("data", "a"), at("data", "b")])).toEqual(["data"]);
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
